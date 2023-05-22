// Code generated by mockery v2.22.1. DO NOT EDIT.

package mocks

import (
	common "github.com/ethereum/go-ethereum/common"
	assets "github.com/smartcontractkit/chainlink/v2/core/assets"

	config "github.com/smartcontractkit/chainlink/v2/core/config"

	mock "github.com/stretchr/testify/mock"

	time "time"
)

// Config is an autogenerated mock type for the Config type
type Config struct {
	mock.Mock
}

// BlockHistoryEstimatorBatchSize provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorBatchSize() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// BlockHistoryEstimatorBlockDelay provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorBlockDelay() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// BlockHistoryEstimatorBlockHistorySize provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorBlockHistorySize() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// BlockHistoryEstimatorCheckInclusionBlocks provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorCheckInclusionBlocks() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// BlockHistoryEstimatorCheckInclusionPercentile provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorCheckInclusionPercentile() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// BlockHistoryEstimatorEIP1559FeeCapBufferBlocks provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorEIP1559FeeCapBufferBlocks() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// BlockHistoryEstimatorTransactionPercentile provides a mock function with given fields:
func (_m *Config) BlockHistoryEstimatorTransactionPercentile() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// ChainType provides a mock function with given fields:
func (_m *Config) ChainType() config.ChainType {
	ret := _m.Called()

	var r0 config.ChainType
	if rf, ok := ret.Get(0).(func() config.ChainType); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(config.ChainType)
	}

	return r0
}

// DatabaseDefaultQueryTimeout provides a mock function with given fields:
func (_m *Config) DatabaseDefaultQueryTimeout() time.Duration {
	ret := _m.Called()

	var r0 time.Duration
	if rf, ok := ret.Get(0).(func() time.Duration); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(time.Duration)
	}

	return r0
}

// EthTxReaperInterval provides a mock function with given fields:
func (_m *Config) EthTxReaperInterval() time.Duration {
	ret := _m.Called()

	var r0 time.Duration
	if rf, ok := ret.Get(0).(func() time.Duration); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(time.Duration)
	}

	return r0
}

// EthTxReaperThreshold provides a mock function with given fields:
func (_m *Config) EthTxReaperThreshold() time.Duration {
	ret := _m.Called()

	var r0 time.Duration
	if rf, ok := ret.Get(0).(func() time.Duration); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(time.Duration)
	}

	return r0
}

// EthTxResendAfterThreshold provides a mock function with given fields:
func (_m *Config) EthTxResendAfterThreshold() time.Duration {
	ret := _m.Called()

	var r0 time.Duration
	if rf, ok := ret.Get(0).(func() time.Duration); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(time.Duration)
	}

	return r0
}

// EvmEIP1559DynamicFees provides a mock function with given fields:
func (_m *Config) EvmEIP1559DynamicFees() bool {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// EvmFinalityDepth provides a mock function with given fields:
func (_m *Config) EvmFinalityDepth() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// EvmGasBumpPercent provides a mock function with given fields:
func (_m *Config) EvmGasBumpPercent() uint16 {
	ret := _m.Called()

	var r0 uint16
	if rf, ok := ret.Get(0).(func() uint16); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint16)
	}

	return r0
}

// EvmGasBumpThreshold provides a mock function with given fields:
func (_m *Config) EvmGasBumpThreshold() uint64 {
	ret := _m.Called()

	var r0 uint64
	if rf, ok := ret.Get(0).(func() uint64); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint64)
	}

	return r0
}

// EvmGasBumpTxDepth provides a mock function with given fields:
func (_m *Config) EvmGasBumpTxDepth() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// EvmGasBumpWei provides a mock function with given fields:
func (_m *Config) EvmGasBumpWei() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmGasFeeCapDefault provides a mock function with given fields:
func (_m *Config) EvmGasFeeCapDefault() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmGasLimitDefault provides a mock function with given fields:
func (_m *Config) EvmGasLimitDefault() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// EvmGasLimitMax provides a mock function with given fields:
func (_m *Config) EvmGasLimitMax() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// EvmGasLimitMultiplier provides a mock function with given fields:
func (_m *Config) EvmGasLimitMultiplier() float32 {
	ret := _m.Called()

	var r0 float32
	if rf, ok := ret.Get(0).(func() float32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(float32)
	}

	return r0
}

// EvmGasPriceDefault provides a mock function with given fields:
func (_m *Config) EvmGasPriceDefault() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmGasTipCapDefault provides a mock function with given fields:
func (_m *Config) EvmGasTipCapDefault() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmGasTipCapMinimum provides a mock function with given fields:
func (_m *Config) EvmGasTipCapMinimum() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmMaxGasPriceWei provides a mock function with given fields:
func (_m *Config) EvmMaxGasPriceWei() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmMaxInFlightTransactions provides a mock function with given fields:
func (_m *Config) EvmMaxInFlightTransactions() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// EvmMaxQueuedTransactions provides a mock function with given fields:
func (_m *Config) EvmMaxQueuedTransactions() uint64 {
	ret := _m.Called()

	var r0 uint64
	if rf, ok := ret.Get(0).(func() uint64); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint64)
	}

	return r0
}

// EvmMinGasPriceWei provides a mock function with given fields:
func (_m *Config) EvmMinGasPriceWei() *assets.Wei {
	ret := _m.Called()

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func() *assets.Wei); ok {
		r0 = rf()
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// EvmNonceAutoSync provides a mock function with given fields:
func (_m *Config) EvmNonceAutoSync() bool {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// EvmRPCDefaultBatchSize provides a mock function with given fields:
func (_m *Config) EvmRPCDefaultBatchSize() uint32 {
	ret := _m.Called()

	var r0 uint32
	if rf, ok := ret.Get(0).(func() uint32); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(uint32)
	}

	return r0
}

// EvmUseForwarders provides a mock function with given fields:
func (_m *Config) EvmUseForwarders() bool {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// GasEstimatorMode provides a mock function with given fields:
func (_m *Config) GasEstimatorMode() string {
	ret := _m.Called()

	var r0 string
	if rf, ok := ret.Get(0).(func() string); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(string)
	}

	return r0
}

// KeySpecificMaxGasPriceWei provides a mock function with given fields: addr
func (_m *Config) KeySpecificMaxGasPriceWei(addr common.Address) *assets.Wei {
	ret := _m.Called(addr)

	var r0 *assets.Wei
	if rf, ok := ret.Get(0).(func(common.Address) *assets.Wei); ok {
		r0 = rf(addr)
	} else {
		if ret.Get(0) != nil {
			r0 = ret.Get(0).(*assets.Wei)
		}
	}

	return r0
}

// LogSQL provides a mock function with given fields:
func (_m *Config) LogSQL() bool {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	return r0
}

// TriggerFallbackDBPollInterval provides a mock function with given fields:
func (_m *Config) TriggerFallbackDBPollInterval() time.Duration {
	ret := _m.Called()

	var r0 time.Duration
	if rf, ok := ret.Get(0).(func() time.Duration); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(time.Duration)
	}

	return r0
}

type mockConstructorTestingTNewConfig interface {
	mock.TestingT
	Cleanup(func())
}

// NewConfig creates a new instance of Config. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewConfig(t mockConstructorTestingTNewConfig) *Config {
	mock := &Config{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
