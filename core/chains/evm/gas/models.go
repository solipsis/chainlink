package gas

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/pkg/errors"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	bigmath "github.com/smartcontractkit/chainlink-common/pkg/utils/big_math"

	"github.com/smartcontractkit/chainlink/v2/common/config"
	commonfee "github.com/smartcontractkit/chainlink/v2/common/fee"
	feetypes "github.com/smartcontractkit/chainlink/v2/common/fee/types"
	commontypes "github.com/smartcontractkit/chainlink/v2/common/types"
	"github.com/smartcontractkit/chainlink/v2/core/chains/evm/assets"
	evmclient "github.com/smartcontractkit/chainlink/v2/core/chains/evm/client"
	evmconfig "github.com/smartcontractkit/chainlink/v2/core/chains/evm/config"
	"github.com/smartcontractkit/chainlink/v2/core/chains/evm/gas/rollups"
	"github.com/smartcontractkit/chainlink/v2/core/chains/evm/label"
	evmtypes "github.com/smartcontractkit/chainlink/v2/core/chains/evm/types"
)

// EvmFeeEstimator provides a unified interface that wraps EvmEstimator and can determine if legacy or dynamic fee estimation should be used
//
//go:generate mockery --quiet --name EvmFeeEstimator --output ./mocks/ --case=underscore
type EvmFeeEstimator interface {
	services.Service
	commontypes.HeadTrackable[*evmtypes.Head, common.Hash]

	// L1Oracle returns the L1 gas price oracle only if the chain has one, e.g. OP stack L2s and Arbitrum.
	L1Oracle() rollups.L1Oracle
	GetFee(ctx context.Context, calldata []byte, feeLimit uint32, maxFeePrice *assets.Wei, opts ...feetypes.Opt) (fee EvmFee, chainSpecificFeeLimit uint32, err error)
	BumpFee(ctx context.Context, originalFee EvmFee, feeLimit uint32, maxFeePrice *assets.Wei, attempts []EvmPriorAttempt) (bumpedFee EvmFee, chainSpecificFeeLimit uint32, err error)

	// GetMaxCost returns the total value = max price x fee units + transferred value
	GetMaxCost(ctx context.Context, amount assets.Eth, calldata []byte, feeLimit uint32, maxFeePrice *assets.Wei, opts ...feetypes.Opt) (*big.Int, error)
}

// NewEstimator returns the estimator for a given config
func NewEstimator(lggr logger.Logger, ethClient evmclient.Client, cfg Config, geCfg evmconfig.GasEstimator) EvmFeeEstimator {
	bh := geCfg.BlockHistory()
	s := geCfg.Mode()
	lggr.Infow(fmt.Sprintf("Initializing EVM gas estimator in mode: %s", s),
		"estimatorMode", s,
		"batchSize", bh.BatchSize(),
		"blockDelay", bh.BlockDelay(),
		"blockHistorySize", bh.BlockHistorySize(),
		"eip1559FeeCapBufferBlocks", bh.EIP1559FeeCapBufferBlocks(),
		"transactionPercentile", bh.TransactionPercentile(),
		"eip1559DynamicFees", geCfg.EIP1559DynamicFees(),
		"gasBumpPercent", geCfg.BumpPercent(),
		"gasBumpThreshold", geCfg.BumpThreshold(),
		"bumpMin", geCfg.BumpMin(),
		"feeCapDefault", geCfg.FeeCapDefault(),
		"limitMultiplier", geCfg.LimitMultiplier(),
		"priceDefault", geCfg.PriceDefault(),
		"tipCapDefault", geCfg.TipCapDefault(),
		"tipCapMin", geCfg.TipCapMin(),
		"priceMax", geCfg.PriceMax(),
		"priceMin", geCfg.PriceMin(),
	)
	df := geCfg.EIP1559DynamicFees()

	// create l1Oracle only if it is supported for the chain
	var l1Oracle rollups.L1Oracle
	if rollups.IsRollupWithL1Support(cfg.ChainType()) {
		l1Oracle = rollups.NewL1GasPriceOracle(lggr, ethClient, cfg.ChainType())
	}
	switch s {
	case "Arbitrum":
		return NewWrappedEvmEstimator(NewArbitrumEstimator(lggr, geCfg, ethClient, ethClient), df, l1Oracle)
	case "BlockHistory":
		return NewWrappedEvmEstimator(NewBlockHistoryEstimator(lggr, ethClient, cfg, geCfg, bh, *ethClient.ConfiguredChainID()), df, l1Oracle)
	case "FixedPrice":
		return NewWrappedEvmEstimator(NewFixedPriceEstimator(geCfg, bh, lggr), df, l1Oracle)
	case "L2Suggested", "SuggestedPrice":
		return NewWrappedEvmEstimator(NewSuggestedPriceEstimator(lggr, ethClient), df, l1Oracle)
	default:
		lggr.Warnf("GasEstimator: unrecognised mode '%s', falling back to FixedPriceEstimator", s)
		return NewWrappedEvmEstimator(NewFixedPriceEstimator(geCfg, bh, lggr), df, l1Oracle)
	}
}

// DynamicFee encompasses both FeeCap and TipCap for EIP1559 transactions
type DynamicFee struct {
	FeeCap *assets.Wei
	TipCap *assets.Wei
}

type EvmPriorAttempt struct {
	ChainSpecificFeeLimit   uint32
	BroadcastBeforeBlockNum *int64
	TxHash                  common.Hash
	TxType                  int
	GasPrice                *assets.Wei
	DynamicFee              DynamicFee
}

// Estimator provides an interface for estimating gas price and limit
//
//go:generate mockery --quiet --name EvmEstimator --output ./mocks/ --case=underscore
type EvmEstimator interface {
	commontypes.HeadTrackable[*evmtypes.Head, common.Hash]
	services.Service

	// GetLegacyGas Calculates initial gas fee for non-EIP1559 transaction
	// maxGasPriceWei parameter is the highest possible gas fee cap that the function will return
	GetLegacyGas(ctx context.Context, calldata []byte, gasLimit uint32, maxGasPriceWei *assets.Wei, opts ...feetypes.Opt) (gasPrice *assets.Wei, chainSpecificGasLimit uint32, err error)
	// BumpLegacyGas Increases gas price and/or limit for non-EIP1559 transactions
	// if the bumped gas fee is greater than maxGasPriceWei, the method returns an error
	// attempts must:
	//   - be sorted in order from highest price to lowest price
	//   - all be of transaction type 0x0 or 0x1
	BumpLegacyGas(ctx context.Context, originalGasPrice *assets.Wei, gasLimit uint32, maxGasPriceWei *assets.Wei, attempts []EvmPriorAttempt) (bumpedGasPrice *assets.Wei, chainSpecificGasLimit uint32, err error)
	// GetDynamicFee Calculates initial gas fee for gas for EIP1559 transactions
	// maxGasPriceWei parameter is the highest possible gas fee cap that the function will return
	GetDynamicFee(ctx context.Context, gasLimit uint32, maxGasPriceWei *assets.Wei) (fee DynamicFee, chainSpecificGasLimit uint32, err error)
	// BumpDynamicFee Increases gas price and/or limit for non-EIP1559 transactions
	// if the bumped gas fee or tip caps are greater than maxGasPriceWei, the method returns an error
	// attempts must:
	//   - be sorted in order from highest price to lowest price
	//   - all be of transaction type 0x2
	BumpDynamicFee(ctx context.Context, original DynamicFee, gasLimit uint32, maxGasPriceWei *assets.Wei, attempts []EvmPriorAttempt) (bumped DynamicFee, chainSpecificGasLimit uint32, err error)
}

var _ feetypes.Fee = (*EvmFee)(nil)

type EvmFee struct {
	// legacy fees
	Legacy *assets.Wei

	// dynamic/EIP1559 fees
	DynamicFeeCap *assets.Wei
	DynamicTipCap *assets.Wei
}

func (fee EvmFee) String() string {
	return fmt.Sprintf("{Legacy: %s, DynamicFeeCap: %s, DynamicTipCap: %s}", fee.Legacy, fee.DynamicFeeCap, fee.DynamicTipCap)
}

func (fee EvmFee) ValidDynamic() bool {
	return fee.DynamicFeeCap != nil && fee.DynamicTipCap != nil
}

// WrappedEvmEstimator provides a struct that wraps the EVM specific dynamic and legacy estimators into one estimator that conforms to the generic FeeEstimator
type WrappedEvmEstimator struct {
	services.StateMachine
	EvmEstimator
	EIP1559Enabled bool
	l1Oracle       rollups.L1Oracle
}

var _ EvmFeeEstimator = (*WrappedEvmEstimator)(nil)

func NewWrappedEvmEstimator(e EvmEstimator, eip1559Enabled bool, l1Oracle rollups.L1Oracle) EvmFeeEstimator {
	return &WrappedEvmEstimator{
		EvmEstimator:   e,
		EIP1559Enabled: eip1559Enabled,
		l1Oracle:       l1Oracle,
	}
}

func (e *WrappedEvmEstimator) Name() string {
	return fmt.Sprintf("WrappedEvmEstimator(%s)", e.EvmEstimator.Name())
}

func (e *WrappedEvmEstimator) Start(ctx context.Context) error {
	return e.StartOnce(e.Name(), func() error {
		if err := e.EvmEstimator.Start(ctx); err != nil {
			return errors.Wrap(err, "failed to start EVMEstimator")
		}
		if e.l1Oracle != nil {
			if err := e.l1Oracle.Start(ctx); err != nil {
				return errors.Wrap(err, "failed to start L1Oracle")
			}
		}
		return nil
	})
}
func (e *WrappedEvmEstimator) Close() error {
	return e.StopOnce(e.Name(), func() error {
		var errEVM, errOracle error

		errEVM = errors.Wrap(e.EvmEstimator.Close(), "failed to stop EVMEstimator")
		if e.l1Oracle != nil {
			errOracle = errors.Wrap(e.l1Oracle.Close(), "failed to stop L1Oracle")
		}

		if errEVM != nil {
			return errEVM
		}
		return errOracle
	})
}

func (e *WrappedEvmEstimator) Ready() error {
	var errEVM, errOracle error

	errEVM = e.EvmEstimator.Ready()
	if e.l1Oracle != nil {
		errOracle = e.l1Oracle.Ready()
	}

	if errEVM != nil {
		return errEVM
	}
	return errOracle
}

func (e *WrappedEvmEstimator) HealthReport() map[string]error {
	report := map[string]error{e.Name(): e.Healthy()}
	services.CopyHealth(report, e.EvmEstimator.HealthReport())
	if e.l1Oracle != nil {
		services.CopyHealth(report, e.l1Oracle.HealthReport())
	}

	return report
}

func (e *WrappedEvmEstimator) L1Oracle() rollups.L1Oracle {
	return e.l1Oracle
}

func (e *WrappedEvmEstimator) GetFee(ctx context.Context, calldata []byte, feeLimit uint32, maxFeePrice *assets.Wei, opts ...feetypes.Opt) (fee EvmFee, chainSpecificFeeLimit uint32, err error) {
	// get dynamic fee
	if e.EIP1559Enabled {
		var dynamicFee DynamicFee
		dynamicFee, chainSpecificFeeLimit, err = e.EvmEstimator.GetDynamicFee(ctx, feeLimit, maxFeePrice)
		fee.DynamicFeeCap = dynamicFee.FeeCap
		fee.DynamicTipCap = dynamicFee.TipCap
		return
	}

	// get legacy fee
	fee.Legacy, chainSpecificFeeLimit, err = e.EvmEstimator.GetLegacyGas(ctx, calldata, feeLimit, maxFeePrice, opts...)
	return
}

func (e *WrappedEvmEstimator) GetMaxCost(ctx context.Context, amount assets.Eth, calldata []byte, feeLimit uint32, maxFeePrice *assets.Wei, opts ...feetypes.Opt) (*big.Int, error) {
	fees, gasLimit, err := e.GetFee(ctx, calldata, feeLimit, maxFeePrice, opts...)
	if err != nil {
		return nil, err
	}

	var gasPrice *assets.Wei
	if e.EIP1559Enabled {
		gasPrice = fees.DynamicFeeCap
	} else {
		gasPrice = fees.Legacy
	}

	fee := new(big.Int).Mul(gasPrice.ToInt(), big.NewInt(int64(gasLimit)))
	amountWithFees := new(big.Int).Add(amount.ToInt(), fee)
	return amountWithFees, nil
}

func (e *WrappedEvmEstimator) BumpFee(ctx context.Context, originalFee EvmFee, feeLimit uint32, maxFeePrice *assets.Wei, attempts []EvmPriorAttempt) (bumpedFee EvmFee, chainSpecificFeeLimit uint32, err error) {
	// validate only 1 fee type is present
	if (!originalFee.ValidDynamic() && originalFee.Legacy == nil) || (originalFee.ValidDynamic() && originalFee.Legacy != nil) {
		err = errors.New("only one dynamic or legacy fee can be defined")
		return
	}

	// bump fee based on what fee the tx has previously used (not based on config)
	// bump dynamic original
	if originalFee.ValidDynamic() {
		var bumpedDynamic DynamicFee
		bumpedDynamic, chainSpecificFeeLimit, err = e.EvmEstimator.BumpDynamicFee(ctx,
			DynamicFee{
				TipCap: originalFee.DynamicTipCap,
				FeeCap: originalFee.DynamicFeeCap,
			}, feeLimit, maxFeePrice, attempts)
		bumpedFee.DynamicFeeCap = bumpedDynamic.FeeCap
		bumpedFee.DynamicTipCap = bumpedDynamic.TipCap
		return
	}

	// bump legacy fee
	bumpedFee.Legacy, chainSpecificFeeLimit, err = e.EvmEstimator.BumpLegacyGas(ctx, originalFee.Legacy, feeLimit, maxFeePrice, attempts)
	return
}

// Config defines an interface for configuration in the gas package
//
//go:generate mockery --quiet --name Config --output ./mocks/ --case=underscore
type Config interface {
	ChainType() config.ChainType
	FinalityDepth() uint32
	FinalityTagEnabled() bool
}

type GasEstimatorConfig interface {
	EIP1559DynamicFees() bool
	BumpPercent() uint16
	BumpThreshold() uint64
	BumpMin() *assets.Wei
	FeeCapDefault() *assets.Wei
	LimitMax() uint32
	LimitMultiplier() float32
	PriceDefault() *assets.Wei
	TipCapDefault() *assets.Wei
	TipCapMin() *assets.Wei
	PriceMin() *assets.Wei
	PriceMax() *assets.Wei
	Mode() string
}

type BlockHistoryConfig interface {
	evmconfig.BlockHistory
}

// Int64ToHex converts an int64 into go-ethereum's hex representation
func Int64ToHex(n int64) string {
	return hexutil.EncodeBig(big.NewInt(n))
}

// HexToInt64 performs the inverse of Int64ToHex
// Returns 0 on invalid input
func HexToInt64(input interface{}) int64 {
	switch v := input.(type) {
	case string:
		big, err := hexutil.DecodeBig(v)
		if err != nil {
			return 0
		}
		return big.Int64()
	case []byte:
		big, err := hexutil.DecodeBig(string(v))
		if err != nil {
			return 0
		}
		return big.Int64()
	default:
		return 0
	}
}

// BumpLegacyGasPriceOnly will increase the price and apply multiplier to the gas limit
func BumpLegacyGasPriceOnly(cfg bumpConfig, lggr logger.SugaredLogger, currentGasPrice, originalGasPrice *assets.Wei, originalGasLimit uint32, maxGasPriceWei *assets.Wei) (gasPrice *assets.Wei, chainSpecificGasLimit uint32, err error) {
	gasPrice, err = bumpGasPrice(cfg, lggr, currentGasPrice, originalGasPrice, maxGasPriceWei)
	if err != nil {
		return nil, 0, err
	}
	chainSpecificGasLimit, err = commonfee.ApplyMultiplier(originalGasLimit, cfg.LimitMultiplier())
	return
}

// bumpGasPrice computes the next gas price to attempt as the largest of:
// - A configured percentage bump (EVM.GasEstimator.BumpPercent) on top of the baseline price.
// - A configured fixed amount of Wei (ETH_GAS_PRICE_WEI) on top of the baseline price.
// The baseline price is the maximum of the previous gas price attempt and the node's current gas price.
func bumpGasPrice(cfg bumpConfig, lggr logger.SugaredLogger, currentGasPrice, originalGasPrice, maxGasPriceWei *assets.Wei) (*assets.Wei, error) {
	maxGasPrice := getMaxGasPrice(maxGasPriceWei, cfg.PriceMax())
	bumpedGasPrice := bumpFeePrice(originalGasPrice, cfg.BumpPercent(), cfg.BumpMin())

	// Update bumpedGasPrice if currentGasPrice is higher than bumpedGasPrice and within maxGasPrice
	bumpedGasPrice = maxBumpedFee(lggr, currentGasPrice, bumpedGasPrice, maxGasPrice, "gas price")

	if bumpedGasPrice.Cmp(maxGasPrice) > 0 {
		return maxGasPrice, errors.Wrapf(commonfee.ErrBumpFeeExceedsLimit, "bumped gas price of %s would exceed configured max gas price of %s (original price was %s). %s",
			bumpedGasPrice.String(), maxGasPrice, originalGasPrice.String(), label.NodeConnectivityProblemWarning)
	} else if bumpedGasPrice.Cmp(originalGasPrice) == 0 {
		// NOTE: This really shouldn't happen since we enforce minimums for
		// EVM.GasEstimator.BumpPercent and EVM.GasEstimator.BumpMin in the config validation,
		// but it's here anyway for a "belts and braces" approach
		return bumpedGasPrice, errors.Wrapf(commonfee.ErrBump, "bumped gas price of %s is equal to original gas price of %s."+
			" ACTION REQUIRED: This is a configuration error, you must increase either "+
			"EVM.GasEstimator.BumpPercent or EVM.GasEstimator.BumpMin", bumpedGasPrice.String(), originalGasPrice.String())
	}
	return bumpedGasPrice, nil
}

// BumpDynamicFeeOnly bumps the tip cap and max gas price if necessary
func BumpDynamicFeeOnly(config bumpConfig, feeCapBufferBlocks uint16, lggr logger.SugaredLogger, currentTipCap, currentBaseFee *assets.Wei, originalFee DynamicFee, originalGasLimit uint32, maxGasPriceWei *assets.Wei) (bumped DynamicFee, chainSpecificGasLimit uint32, err error) {
	bumped, err = bumpDynamicFee(config, feeCapBufferBlocks, lggr, currentTipCap, currentBaseFee, originalFee, maxGasPriceWei)
	if err != nil {
		return bumped, 0, err
	}
	chainSpecificGasLimit, err = commonfee.ApplyMultiplier(originalGasLimit, config.LimitMultiplier())
	return
}

// bumpDynamicFee computes the next tip cap to attempt as the largest of:
// - A configured percentage bump (EVM.GasEstimator.BumpPercent) on top of the baseline tip cap.
// - A configured fixed amount of Wei (ETH_GAS_PRICE_WEI) on top of the baseline tip cap.
// The baseline tip cap is the maximum of the previous tip cap attempt and the node's current tip cap.
// It increases the max fee cap by GasBumpPercent
//
// NOTE: We would prefer to have set a large FeeCap and leave it fixed, bumping
// the Tip only. Unfortunately due to a flaw of how EIP-1559 is implemented we
// have to bump FeeCap by at least 10% each time we bump the tip cap.
// See: https://github.com/ethereum/go-ethereum/issues/24284
func bumpDynamicFee(cfg bumpConfig, feeCapBufferBlocks uint16, lggr logger.SugaredLogger, currentTipCap, currentBaseFee *assets.Wei, originalFee DynamicFee, maxGasPriceWei *assets.Wei) (bumpedFee DynamicFee, err error) {
	maxGasPrice := getMaxGasPrice(maxGasPriceWei, cfg.PriceMax())
	baselineTipCap := assets.MaxWei(originalFee.TipCap, cfg.TipCapDefault())
	bumpedTipCap := bumpFeePrice(baselineTipCap, cfg.BumpPercent(), cfg.BumpMin())

	// Update bumpedTipCap if currentTipCap is higher than bumpedTipCap and within maxGasPrice
	bumpedTipCap = maxBumpedFee(lggr, currentTipCap, bumpedTipCap, maxGasPrice, "tip cap")

	if bumpedTipCap.Cmp(maxGasPrice) > 0 {
		return bumpedFee, errors.Wrapf(commonfee.ErrBumpFeeExceedsLimit, "bumped tip cap of %s would exceed configured max gas price of %s (original fee: tip cap %s, fee cap %s). %s",
			bumpedTipCap.String(), maxGasPrice, originalFee.TipCap.String(), originalFee.FeeCap.String(), label.NodeConnectivityProblemWarning)
	} else if bumpedTipCap.Cmp(originalFee.TipCap) <= 0 {
		// NOTE: This really shouldn't happen since we enforce minimums for
		// EVM.GasEstimator.BumpPercent and EVM.GasEstimator.BumpMin in the config validation,
		// but it's here anyway for a "belts and braces" approach
		return bumpedFee, errors.Wrapf(commonfee.ErrBump, "bumped gas tip cap of %s is less than or equal to original gas tip cap of %s."+
			" ACTION REQUIRED: This is a configuration error, you must increase either "+
			"EVM.GasEstimator.BumpPercent or EVM.GasEstimator.BumpMin", bumpedTipCap.String(), originalFee.TipCap.String())
	}

	// Always bump the FeeCap by at least the bump percentage (should be greater than or
	// equal to than geth's configured bump minimum which is 10%)
	// See: https://github.com/ethereum/go-ethereum/blob/bff330335b94af3643ac2fb809793f77de3069d4/core/tx_list.go#L298
	bumpedFeeCap := bumpFeePrice(originalFee.FeeCap, cfg.BumpPercent(), cfg.BumpMin())

	if currentBaseFee != nil {
		if currentBaseFee.Cmp(maxGasPrice) > 0 {
			lggr.Warnf("Ignoring current base fee of %s which is greater than max gas price of %s", currentBaseFee.String(), maxGasPrice.String())
		} else {
			currentFeeCap := calcFeeCap(currentBaseFee, int(feeCapBufferBlocks), bumpedTipCap, maxGasPrice)
			bumpedFeeCap = assets.WeiMax(bumpedFeeCap, currentFeeCap)
		}
	}

	if bumpedFeeCap.Cmp(maxGasPrice) > 0 {
		return bumpedFee, errors.Wrapf(commonfee.ErrBumpFeeExceedsLimit, "bumped fee cap of %s would exceed configured max gas price of %s (original fee: tip cap %s, fee cap %s). %s",
			bumpedFeeCap.String(), maxGasPrice, originalFee.TipCap.String(), originalFee.FeeCap.String(), label.NodeConnectivityProblemWarning)
	}

	return DynamicFee{FeeCap: bumpedFeeCap, TipCap: bumpedTipCap}, nil
}

func bumpFeePrice(originalFeePrice *assets.Wei, feeBumpPercent uint16, feeBumpUnits *assets.Wei) *assets.Wei {
	bumpedFeePrice := assets.MaxWei(
		originalFeePrice.AddPercentage(feeBumpPercent),
		originalFeePrice.Add(feeBumpUnits),
	)
	return bumpedFeePrice
}

func maxBumpedFee(lggr logger.SugaredLogger, currentFeePrice, bumpedFeePrice, maxGasPrice *assets.Wei, feeType string) *assets.Wei {
	if currentFeePrice != nil {
		if currentFeePrice.Cmp(maxGasPrice) > 0 {
			// Shouldn't happen because the estimator should not be allowed to
			// estimate a higher gas than the maximum allowed
			lggr.AssumptionViolationf("Ignoring current %s of %s that would exceed max %s of %s", feeType, currentFeePrice.String(), feeType, maxGasPrice.String())
		} else if bumpedFeePrice.Cmp(currentFeePrice) < 0 {
			// If the current gas price is higher than the old price bumped, use that instead
			bumpedFeePrice = currentFeePrice
		}
	}
	return bumpedFeePrice
}

func getMaxGasPrice(userSpecifiedMax, maxGasPriceWei *assets.Wei) *assets.Wei {
	return assets.NewWei(bigmath.Min(userSpecifiedMax.ToInt(), maxGasPriceWei.ToInt()))
}

func capGasPrice(calculatedGasPrice, userSpecifiedMax, maxGasPriceWei *assets.Wei) *assets.Wei {
	maxGasPrice := commonfee.CalculateFee(calculatedGasPrice.ToInt(), userSpecifiedMax.ToInt(), maxGasPriceWei.ToInt())
	return assets.NewWei(maxGasPrice)
}
