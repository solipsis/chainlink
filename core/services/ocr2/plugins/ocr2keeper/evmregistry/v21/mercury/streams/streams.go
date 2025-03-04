package streams

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/patrickmn/go-cache"

	ocr2keepers "github.com/smartcontractkit/chainlink-automation/pkg/v3/types"

	"github.com/smartcontractkit/chainlink-common/pkg/services"

	iregistry21 "github.com/smartcontractkit/chainlink/v2/core/gethwrappers/generated/i_keeper_registry_master_wrapper_2_1"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/ocr2/plugins/ocr2keeper/evmregistry/v21/core"
	"github.com/smartcontractkit/chainlink/v2/core/services/ocr2/plugins/ocr2keeper/evmregistry/v21/mercury"
	v02 "github.com/smartcontractkit/chainlink/v2/core/services/ocr2/plugins/ocr2keeper/evmregistry/v21/mercury/v02"
	v03 "github.com/smartcontractkit/chainlink/v2/core/services/ocr2/plugins/ocr2keeper/evmregistry/v21/mercury/v03"
	"github.com/smartcontractkit/chainlink/v2/core/utils"
)

type Lookup interface {
	Lookup(ctx context.Context, checkResults []ocr2keepers.CheckResult) []ocr2keepers.CheckResult
}

type latestBlockProvider interface {
	LatestBlock() *ocr2keepers.BlockKey
}

type streamsRegistry interface {
	GetUpkeepPrivilegeConfig(opts *bind.CallOpts, upkeepId *big.Int) ([]byte, error)
	CheckCallback(opts *bind.CallOpts, id *big.Int, values [][]byte, extraData []byte) (iregistry21.CheckCallback, error)
	Address() common.Address
}

type contextCaller interface {
	CallContext(ctx context.Context, result interface{}, method string, args ...interface{}) error
}

type streams struct {
	services.StateMachine
	packer          mercury.Packer
	mercuryConfig   mercury.MercuryConfigProvider
	abi             abi.ABI
	blockSubscriber latestBlockProvider
	registry        streamsRegistry
	client          contextCaller
	lggr            logger.Logger
	threadCtrl      utils.ThreadControl
	v02Client       mercury.MercuryClient
	v03Client       mercury.MercuryClient
}

// UpkeepPrivilegeConfig represents the administrative offchain config for each upkeep. It can be set by s_upkeepPrivilegeManager
// role on the registry. Upkeeps allowed to use Mercury server will have this set to true.
type UpkeepPrivilegeConfig struct {
	MercuryEnabled bool `json:"mercuryEnabled"`
}

func NewStreamsLookup(
	packer mercury.Packer,
	mercuryConfig mercury.MercuryConfigProvider,
	blockSubscriber latestBlockProvider,
	client contextCaller,
	registry streamsRegistry,
	lggr logger.Logger) *streams {
	httpClient := http.DefaultClient
	threadCtrl := utils.NewThreadControl()
	return &streams{
		packer:          packer,
		mercuryConfig:   mercuryConfig,
		abi:             core.RegistryABI,
		blockSubscriber: blockSubscriber,
		registry:        registry,
		client:          client,
		lggr:            lggr,
		threadCtrl:      threadCtrl,
		v02Client:       v02.NewClient(mercuryConfig, httpClient, threadCtrl, lggr),
		v03Client:       v03.NewClient(mercuryConfig, httpClient, threadCtrl, lggr),
	}
}

// Lookup looks through check upkeep results looking for any that need off chain lookup
func (s *streams) Lookup(ctx context.Context, checkResults []ocr2keepers.CheckResult) []ocr2keepers.CheckResult {
	lookups := map[int]*mercury.StreamsLookup{}
	for i, checkResult := range checkResults {
		s.buildResult(ctx, i, checkResult, checkResults, lookups)
	}

	var wg sync.WaitGroup
	for i, lookup := range lookups {
		wg.Add(1)
		func(i int, lookup *mercury.StreamsLookup) {
			s.threadCtrl.Go(func(ctx context.Context) {
				s.doLookup(ctx, &wg, lookup, i, checkResults)
			})
		}(i, lookup)
	}
	wg.Wait()

	// don't surface error to plugin bc StreamsLookup process should be self-contained.
	return checkResults
}

// buildResult checks if the upkeep is allowed by Mercury and builds a streams lookup request from the check result
func (s *streams) buildResult(ctx context.Context, i int, checkResult ocr2keepers.CheckResult, checkResults []ocr2keepers.CheckResult, lookups map[int]*mercury.StreamsLookup) {
	lookupLggr := s.lggr.With("where", "StreamsLookup")
	if checkResult.IneligibilityReason != uint8(mercury.MercuryUpkeepFailureReasonTargetCheckReverted) {
		// Streams Lookup only works when upkeep target check reverts
		return
	}

	block := big.NewInt(int64(checkResult.Trigger.BlockNumber))
	upkeepId := checkResult.UpkeepID

	if s.mercuryConfig.Credentials() == nil {
		lookupLggr.Errorf("at block %d upkeep %s tries to access mercury server but mercury credential is not configured", block, upkeepId)
		return
	}

	// Try to decode the revert error into streams lookup format. User upkeeps can revert with any reason, see if they
	// tried to call mercury
	lookupLggr.Infof("at block %d upkeep %s trying to DecodeStreamsLookupRequest performData=%s", block, upkeepId, hexutil.Encode(checkResults[i].PerformData))
	streamsLookupErr, err := s.packer.DecodeStreamsLookupRequest(checkResult.PerformData)
	if err != nil {
		lookupLggr.Debugf("at block %d upkeep %s DecodeStreamsLookupRequest failed: %v", block, upkeepId, err)
		// user contract did not revert with StreamsLookup error
		return
	}
	streamsLookupResponse := &mercury.StreamsLookup{StreamsLookupError: streamsLookupErr}

	if len(streamsLookupResponse.Feeds) == 0 {
		checkResults[i].IneligibilityReason = uint8(mercury.MercuryUpkeepFailureReasonInvalidRevertDataInput)
		lookupLggr.Debugf("at block %s upkeep %s has empty feeds array", block, upkeepId)
		return
	}

	// mercury permission checking for v0.3 is done by mercury server
	if streamsLookupResponse.IsMercuryV02() {
		// check permission on the registry for mercury v0.2
		opts := s.buildCallOpts(ctx, block)
		if state, reason, retryable, allowed, err := s.allowedToUseMercury(opts, upkeepId.BigInt()); err != nil {
			lookupLggr.Warnf("at block %s upkeep %s failed to query mercury allow list: %s", block, upkeepId, err)
			checkResults[i].PipelineExecutionState = uint8(state)
			checkResults[i].IneligibilityReason = uint8(reason)
			checkResults[i].Retryable = retryable
			return
		} else if !allowed {
			lookupLggr.Debugf("at block %d upkeep %s NOT allowed to query Mercury server", block, upkeepId)
			checkResults[i].IneligibilityReason = uint8(mercury.MercuryUpkeepFailureReasonMercuryAccessNotAllowed)
			return
		}
	} else if streamsLookupResponse.IsMercuryVersionUnkown() {
		// if mercury version cannot be determined, set failure reason
		lookupLggr.Debugf("at block %d upkeep %s NOT allowed to query Mercury server", block, upkeepId)
		checkResults[i].IneligibilityReason = uint8(mercury.MercuryUpkeepFailureReasonInvalidRevertDataInput)
		return
	}

	streamsLookupResponse.UpkeepId = upkeepId.BigInt()
	// the block here is exclusively used to call checkCallback at this block, not to be confused with the block number
	// in the revert for mercury v0.2, which is denoted by time in the struct bc starting from v0.3, only timestamp will be supported
	streamsLookupResponse.Block = uint64(block.Int64())
	lookupLggr.Infof("at block %d upkeep %s DecodeStreamsLookupRequest feedKey=%s timeKey=%s feeds=%v time=%s extraData=%s", block, upkeepId, streamsLookupResponse.FeedParamKey, streamsLookupResponse.TimeParamKey, streamsLookupResponse.Feeds, streamsLookupResponse.Time, hexutil.Encode(streamsLookupResponse.ExtraData))
	lookups[i] = streamsLookupResponse
}

func (s *streams) doLookup(ctx context.Context, wg *sync.WaitGroup, lookup *mercury.StreamsLookup, i int, checkResults []ocr2keepers.CheckResult) {
	defer wg.Done()

	state, reason, values, retryable, retryInterval, err := mercury.NoPipelineError, mercury.MercuryUpkeepFailureReasonInvalidRevertDataInput, [][]byte{}, false, 0*time.Second, fmt.Errorf("invalid revert data input: feed param key %s, time param key %s, feeds %s", lookup.FeedParamKey, lookup.TimeParamKey, lookup.Feeds)
	pluginRetryKey := generatePluginRetryKey(checkResults[i].WorkID, lookup.Block)

	if lookup.IsMercuryV02() {
		state, reason, values, retryable, retryInterval, err = s.v02Client.DoRequest(ctx, lookup, pluginRetryKey)
	} else if lookup.IsMercuryV03() {
		state, reason, values, retryable, retryInterval, err = s.v03Client.DoRequest(ctx, lookup, pluginRetryKey)
	}

	if err != nil {
		s.lggr.Errorf("at block %d upkeep %s requested time %s retryable %v retryInterval %s doMercuryRequest: %s", lookup.Block, lookup.UpkeepId, lookup.Time, retryable, retryInterval, err.Error())
		checkResults[i].Retryable = retryable
		checkResults[i].RetryInterval = retryInterval
		checkResults[i].PipelineExecutionState = uint8(state)
		checkResults[i].IneligibilityReason = uint8(reason)
		return
	}

	for j, v := range values {
		s.lggr.Infof("at block %d upkeep %s requested time %s doMercuryRequest values[%d]: %s", lookup.Block, lookup.UpkeepId, lookup.Time, j, hexutil.Encode(v))
	}

	state, retryable, mercuryBytes, err := s.checkCallback(ctx, values, lookup)
	if err != nil {
		s.lggr.Errorf("at block %d upkeep %s checkCallback err: %s", lookup.Block, lookup.UpkeepId, err.Error())
		checkResults[i].Retryable = retryable
		checkResults[i].PipelineExecutionState = uint8(state)
		return
	}
	s.lggr.Infof("at block %d upkeep %s requested time %s checkCallback mercuryBytes: %s", lookup.Block, lookup.UpkeepId, lookup.Time, hexutil.Encode(mercuryBytes))

	unpackCallBackState, needed, performData, failureReason, _, err := s.packer.UnpackCheckCallbackResult(mercuryBytes)
	if err != nil {
		s.lggr.Errorf("at block %d upkeep %s requested time %s UnpackCheckCallbackResult err: %s", lookup.Block, lookup.UpkeepId, lookup.Time, err.Error())
		checkResults[i].PipelineExecutionState = unpackCallBackState
		return
	}

	if failureReason == uint8(mercury.MercuryUpkeepFailureReasonMercuryCallbackReverted) {
		checkResults[i].IneligibilityReason = uint8(mercury.MercuryUpkeepFailureReasonMercuryCallbackReverted)
		s.lggr.Debugf("at block %d upkeep %s requested time %s mercury callback reverts", lookup.Block, lookup.UpkeepId, lookup.Time)
		return
	}

	if !needed {
		checkResults[i].IneligibilityReason = uint8(mercury.MercuryUpkeepFailureReasonUpkeepNotNeeded)
		s.lggr.Debugf("at block %d upkeep %s requested time %s callback reports upkeep not needed", lookup.Block, lookup.UpkeepId, lookup.Time)
		return
	}

	checkResults[i].IneligibilityReason = uint8(mercury.MercuryUpkeepFailureReasonNone)
	checkResults[i].Eligible = true
	checkResults[i].PerformData = performData
	s.lggr.Infof("at block %d upkeep %s requested time %s successful with perform data: %s", lookup.Block, lookup.UpkeepId, lookup.Time, hexutil.Encode(performData))
}

// allowedToUseMercury retrieves upkeep's administrative offchain config and decode a mercuryEnabled bool to indicate if
// this upkeep is allowed to use Mercury service.
func (s *streams) allowedToUseMercury(opts *bind.CallOpts, upkeepId *big.Int) (state mercury.MercuryUpkeepState, reason mercury.MercuryUpkeepFailureReason, retryable bool, allow bool, err error) {
	allowed, ok := s.mercuryConfig.IsUpkeepAllowed(upkeepId.String())
	if ok {
		return mercury.NoPipelineError, mercury.MercuryUpkeepFailureReasonNone, false, allowed.(bool), nil
	}

	payload, err := s.packer.PackGetUpkeepPrivilegeConfig(upkeepId)
	if err != nil {
		// pack error, no retryable
		s.lggr.Warnf("failed to pack getUpkeepPrivilegeConfig data for upkeepId %s: %s", upkeepId, err)

		return mercury.PackUnpackDecodeFailed, mercury.MercuryUpkeepFailureReasonNone, false, false, fmt.Errorf("failed to pack upkeepId: %w", err)
	}

	var resultBytes hexutil.Bytes
	args := map[string]interface{}{
		"to":   s.registry.Address().Hex(),
		"data": hexutil.Bytes(payload),
	}

	// call checkCallback function at the block which OCR3 has agreed upon
	if err = s.client.CallContext(opts.Context, &resultBytes, "eth_call", args, hexutil.EncodeBig(opts.BlockNumber)); err != nil {
		return mercury.RpcFlakyFailure, mercury.MercuryUpkeepFailureReasonNone, true, false, fmt.Errorf("failed to get upkeep privilege config: %v", err)
	}

	var upkeepPrivilegeConfigBytes []byte
	upkeepPrivilegeConfigBytes, err = s.packer.UnpackGetUpkeepPrivilegeConfig(resultBytes)
	if err != nil {
		return mercury.PackUnpackDecodeFailed, mercury.MercuryUpkeepFailureReasonNone, false, false, fmt.Errorf("failed to get upkeep privilege config: %v", err)
	}

	if len(upkeepPrivilegeConfigBytes) == 0 {
		s.mercuryConfig.SetUpkeepAllowed(upkeepId.String(), false, cache.DefaultExpiration)
		return mercury.NoPipelineError, mercury.MercuryUpkeepFailureReasonMercuryAccessNotAllowed, false, false, fmt.Errorf("upkeep privilege config is empty")
	}

	var privilegeConfig UpkeepPrivilegeConfig
	if err = json.Unmarshal(upkeepPrivilegeConfigBytes, &privilegeConfig); err != nil {
		return mercury.MercuryUnmarshalError, mercury.MercuryUpkeepFailureReasonNone, false, false, fmt.Errorf("failed to unmarshal privilege config: %v", err)
	}

	s.mercuryConfig.SetUpkeepAllowed(upkeepId.String(), privilegeConfig.MercuryEnabled, cache.DefaultExpiration)

	return mercury.NoPipelineError, mercury.MercuryUpkeepFailureReasonNone, false, privilegeConfig.MercuryEnabled, nil
}

func (s *streams) checkCallback(ctx context.Context, values [][]byte, lookup *mercury.StreamsLookup) (mercury.MercuryUpkeepState, bool, hexutil.Bytes, error) {
	payload, err := s.abi.Pack("checkCallback", lookup.UpkeepId, values, lookup.ExtraData)
	if err != nil {
		return mercury.PackUnpackDecodeFailed, false, nil, err
	}

	var b hexutil.Bytes
	args := map[string]interface{}{
		"to":   s.registry.Address().Hex(),
		"data": hexutil.Bytes(payload),
	}

	// call checkCallback function at the block which OCR3 has agreed upon
	if err := s.client.CallContext(ctx, &b, "eth_call", args, hexutil.EncodeUint64(lookup.Block)); err != nil {
		return mercury.RpcFlakyFailure, true, nil, err
	}

	return mercury.NoPipelineError, false, b, nil
}

func (s *streams) buildCallOpts(ctx context.Context, block *big.Int) *bind.CallOpts {
	opts := bind.CallOpts{
		Context: ctx,
	}

	if block == nil || block.Int64() == 0 {
		if latestBlock := s.blockSubscriber.LatestBlock(); latestBlock != nil && latestBlock.Number != 0 {
			opts.BlockNumber = big.NewInt(int64(latestBlock.Number))
		}
	} else {
		opts.BlockNumber = block
	}

	return &opts
}

// generatePluginRetryKey returns a plugin retry cache key
func generatePluginRetryKey(workID string, block uint64) string {
	return workID + "|" + fmt.Sprintf("%d", block)
}

func (s *streams) Close() error {
	return s.StopOnce("streams_lookup", func() error {
		s.threadCtrl.Close()
		return nil
	})
}
