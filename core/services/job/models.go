package job

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/pkg/errors"
	"gopkg.in/guregu/null.v4"

	commonassets "github.com/smartcontractkit/chainlink-common/pkg/assets"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/chainlink/v2/core/bridges"
	"github.com/smartcontractkit/chainlink/v2/core/chains/evm/assets"
	"github.com/smartcontractkit/chainlink/v2/core/chains/evm/config/toml"
	clnull "github.com/smartcontractkit/chainlink/v2/core/null"
	"github.com/smartcontractkit/chainlink/v2/core/services/keystore/keys/ethkey"
	"github.com/smartcontractkit/chainlink/v2/core/services/pipeline"
	"github.com/smartcontractkit/chainlink/v2/core/services/relay"
	"github.com/smartcontractkit/chainlink/v2/core/services/signatures/secp256k1"
	"github.com/smartcontractkit/chainlink/v2/core/store/models"
	"github.com/smartcontractkit/chainlink/v2/core/utils"
	"github.com/smartcontractkit/chainlink/v2/core/utils/stringutils"
	"github.com/smartcontractkit/chainlink/v2/core/utils/tomlutils"
)

const (
	Cron                    Type = (Type)(pipeline.CronJobType)
	DirectRequest           Type = (Type)(pipeline.DirectRequestJobType)
	FluxMonitor             Type = (Type)(pipeline.FluxMonitorJobType)
	OffchainReporting       Type = (Type)(pipeline.OffchainReportingJobType)
	OffchainReporting2      Type = (Type)(pipeline.OffchainReporting2JobType)
	Keeper                  Type = (Type)(pipeline.KeeperJobType)
	VRF                     Type = (Type)(pipeline.VRFJobType)
	BlockhashStore          Type = (Type)(pipeline.BlockhashStoreJobType)
	BlockHeaderFeeder       Type = (Type)(pipeline.BlockHeaderFeederJobType)
	LegacyGasStationServer  Type = (Type)(pipeline.LegacyGasStationServerJobType)
	LegacyGasStationSidecar Type = (Type)(pipeline.LegacyGasStationSidecarJobType)
	Webhook                 Type = (Type)(pipeline.WebhookJobType)
	Bootstrap               Type = (Type)(pipeline.BootstrapJobType)
	Gateway                 Type = (Type)(pipeline.GatewayJobType)
)

//revive:disable:redefines-builtin-id
type Type string

func (t Type) String() string {
	return string(t)
}

func (t Type) RequiresPipelineSpec() bool {
	return requiresPipelineSpec[t]
}

func (t Type) SupportsAsync() bool {
	return supportsAsync[t]
}

func (t Type) SchemaVersion() uint32 {
	return schemaVersions[t]
}

var (
	requiresPipelineSpec = map[Type]bool{
		Cron:                    true,
		DirectRequest:           true,
		FluxMonitor:             true,
		OffchainReporting:       false, // bootstrap jobs do not require it
		OffchainReporting2:      false, // bootstrap jobs do not require it
		Keeper:                  false, // observationSource is injected in the upkeep executor
		VRF:                     true,
		Webhook:                 true,
		BlockhashStore:          false,
		BlockHeaderFeeder:       false,
		LegacyGasStationServer:  false,
		LegacyGasStationSidecar: false,
		Bootstrap:               false,
		Gateway:                 false,
	}
	supportsAsync = map[Type]bool{
		Cron:                    true,
		DirectRequest:           true,
		FluxMonitor:             false,
		OffchainReporting:       false,
		OffchainReporting2:      false,
		Keeper:                  true,
		VRF:                     true,
		Webhook:                 true,
		BlockhashStore:          false,
		BlockHeaderFeeder:       false,
		LegacyGasStationServer:  false,
		LegacyGasStationSidecar: false,
		Bootstrap:               false,
		Gateway:                 false,
	}
	schemaVersions = map[Type]uint32{
		Cron:                    1,
		DirectRequest:           1,
		FluxMonitor:             1,
		OffchainReporting:       1,
		OffchainReporting2:      1,
		Keeper:                  1,
		VRF:                     1,
		Webhook:                 1,
		BlockhashStore:          1,
		BlockHeaderFeeder:       1,
		LegacyGasStationServer:  1,
		LegacyGasStationSidecar: 1,
		Bootstrap:               1,
		Gateway:                 1,
	}
)

type Job struct {
	ID                            int32     `toml:"-"`
	ExternalJobID                 uuid.UUID `toml:"externalJobID"`
	OCROracleSpecID               *int32
	OCROracleSpec                 *OCROracleSpec
	OCR2OracleSpecID              *int32
	OCR2OracleSpec                *OCR2OracleSpec
	CronSpecID                    *int32
	CronSpec                      *CronSpec
	DirectRequestSpecID           *int32
	DirectRequestSpec             *DirectRequestSpec
	FluxMonitorSpecID             *int32
	FluxMonitorSpec               *FluxMonitorSpec
	KeeperSpecID                  *int32
	KeeperSpec                    *KeeperSpec
	VRFSpecID                     *int32
	VRFSpec                       *VRFSpec
	WebhookSpecID                 *int32
	WebhookSpec                   *WebhookSpec
	BlockhashStoreSpecID          *int32
	BlockhashStoreSpec            *BlockhashStoreSpec
	BlockHeaderFeederSpecID       *int32
	BlockHeaderFeederSpec         *BlockHeaderFeederSpec
	LegacyGasStationServerSpecID  *int32
	LegacyGasStationServerSpec    *LegacyGasStationServerSpec
	LegacyGasStationSidecarSpecID *int32
	LegacyGasStationSidecarSpec   *LegacyGasStationSidecarSpec
	BootstrapSpec                 *BootstrapSpec
	BootstrapSpecID               *int32
	GatewaySpec                   *GatewaySpec
	GatewaySpecID                 *int32
	EALSpec                       *EALSpec
	EALSpecID                     *int32
	PipelineSpecID                int32
	PipelineSpec                  *pipeline.Spec
	JobSpecErrors                 []SpecError
	Type                          Type
	SchemaVersion                 uint32
	GasLimit                      clnull.Uint32 `toml:"gasLimit"`
	ForwardingAllowed             bool          `toml:"forwardingAllowed"`
	Name                          null.String
	MaxTaskDuration               models.Interval
	Pipeline                      pipeline.Pipeline `toml:"observationSource"`
	CreatedAt                     time.Time
}

func ExternalJobIDEncodeStringToTopic(id uuid.UUID) common.Hash {
	return common.BytesToHash([]byte(strings.Replace(id.String(), "-", "", 4)))
}

func ExternalJobIDEncodeBytesToTopic(id uuid.UUID) common.Hash {
	return common.BytesToHash(common.RightPadBytes(id[:], utils.EVMWordByteLen))
}

// ExternalIDEncodeStringToTopic encodes the external job ID (UUID) into a log topic (32 bytes)
// by taking the string representation of the UUID, removing the dashes
// so that its 32 characters long and then encoding those characters to bytes.
func (j Job) ExternalIDEncodeStringToTopic() common.Hash {
	return ExternalJobIDEncodeStringToTopic(j.ExternalJobID)
}

// ExternalIDEncodeBytesToTopic encodes the external job ID (UUID) into a log topic (32 bytes)
// by taking the 16 bytes underlying the UUID and right padding it.
func (j Job) ExternalIDEncodeBytesToTopic() common.Hash {
	return ExternalJobIDEncodeBytesToTopic(j.ExternalJobID)
}

// SetID takes the id as a string and attempts to convert it to an int32. If
// it succeeds, it will set it as the id on the job
func (j *Job) SetID(value string) error {
	id, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	j.ID = int32(id)
	return nil
}

type SpecError struct {
	ID          int64
	JobID       int32
	Description string
	Occurrences uint
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SetID takes the id as a string and attempts to convert it to an int32. If
// it succeeds, it will set it as the id on the job
func (j *SpecError) SetID(value string) error {
	id, err := stringutils.ToInt64(value)
	if err != nil {
		return err
	}
	j.ID = id
	return nil
}

type PipelineRun struct {
	ID int64 `json:"-"`
}

func (pr PipelineRun) GetID() string {
	return fmt.Sprintf("%v", pr.ID)
}

func (pr *PipelineRun) SetID(value string) error {
	ID, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return err
	}
	pr.ID = int64(ID)
	return nil
}

// OCROracleSpec defines the job spec for OCR jobs.
type OCROracleSpec struct {
	ID                                     int32                `toml:"-"`
	ContractAddress                        ethkey.EIP55Address  `toml:"contractAddress"`
	P2PV2Bootstrappers                     pq.StringArray       `toml:"p2pv2Bootstrappers" db:"p2pv2_bootstrappers"`
	IsBootstrapPeer                        bool                 `toml:"isBootstrapPeer"`
	EncryptedOCRKeyBundleID                *models.Sha256Hash   `toml:"keyBundleID"`
	TransmitterAddress                     *ethkey.EIP55Address `toml:"transmitterAddress"`
	ObservationTimeout                     models.Interval      `toml:"observationTimeout"`
	BlockchainTimeout                      models.Interval      `toml:"blockchainTimeout"`
	ContractConfigTrackerSubscribeInterval models.Interval      `toml:"contractConfigTrackerSubscribeInterval"`
	ContractConfigTrackerPollInterval      models.Interval      `toml:"contractConfigTrackerPollInterval"`
	ContractConfigConfirmations            uint16               `toml:"contractConfigConfirmations"`
	EVMChainID                             *utils.Big           `toml:"evmChainID" db:"evm_chain_id"`
	DatabaseTimeout                        *models.Interval     `toml:"databaseTimeout"`
	ObservationGracePeriod                 *models.Interval     `toml:"observationGracePeriod"`
	ContractTransmitterTransmitTimeout     *models.Interval     `toml:"contractTransmitterTransmitTimeout"`
	CaptureEATelemetry                     bool                 `toml:"captureEATelemetry"`
	CreatedAt                              time.Time            `toml:"-"`
	UpdatedAt                              time.Time            `toml:"-"`
}

// GetID is a getter function that returns the ID of the spec.
func (s OCROracleSpec) GetID() string {
	return fmt.Sprintf("%v", s.ID)
}

// SetID is a setter function that sets the ID of the spec.
func (s *OCROracleSpec) SetID(value string) error {
	ID, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	s.ID = int32(ID)
	return nil
}

// JSONConfig is a Go mapping for JSON based database properties.
type JSONConfig map[string]interface{}

// Bytes returns the raw bytes
func (r JSONConfig) Bytes() []byte {
	b, _ := json.Marshal(r)
	return b
}

// Value returns this instance serialized for database storage.
func (r JSONConfig) Value() (driver.Value, error) {
	return json.Marshal(r)
}

// Scan reads the database value and returns an instance.
func (r *JSONConfig) Scan(value interface{}) error {
	b, ok := value.([]byte)
	if !ok {
		return errors.Errorf("expected bytes got %T", b)
	}
	return json.Unmarshal(b, &r)
}

func (r JSONConfig) MercuryCredentialName() (string, error) {
	url, ok := r["mercuryCredentialName"]
	if !ok {
		return "", nil
	}
	name, ok := url.(string)
	if !ok {
		return "", fmt.Errorf("expected string mercuryCredentialName but got: %T", url)
	}
	return name, nil
}

var ForwardersSupportedPlugins = []types.OCR2PluginType{types.Median, types.DKG, types.OCR2VRF, types.OCR2Keeper, types.Functions}

// OCR2OracleSpec defines the job spec for OCR2 jobs.
// Relay config is chain specific config for a relay (chain adapter).
type OCR2OracleSpec struct {
	ID         int32         `toml:"-"`
	ContractID string        `toml:"contractID"`
	FeedID     *common.Hash  `toml:"feedID"`
	Relay      relay.Network `toml:"relay"`
	// TODO BCF-2442 implement ChainID as top level parameter rathe than buried in RelayConfig.
	ChainID                           string               `toml:"chainID"`
	RelayConfig                       JSONConfig           `toml:"relayConfig"`
	P2PV2Bootstrappers                pq.StringArray       `toml:"p2pv2Bootstrappers"`
	OCRKeyBundleID                    null.String          `toml:"ocrKeyBundleID"`
	MonitoringEndpoint                null.String          `toml:"monitoringEndpoint"`
	TransmitterID                     null.String          `toml:"transmitterID"`
	BlockchainTimeout                 models.Interval      `toml:"blockchainTimeout"`
	ContractConfigTrackerPollInterval models.Interval      `toml:"contractConfigTrackerPollInterval"`
	ContractConfigConfirmations       uint16               `toml:"contractConfigConfirmations"`
	PluginConfig                      JSONConfig           `toml:"pluginConfig"`
	PluginType                        types.OCR2PluginType `toml:"pluginType"`
	CreatedAt                         time.Time            `toml:"-"`
	UpdatedAt                         time.Time            `toml:"-"`
	CaptureEATelemetry                bool                 `toml:"captureEATelemetry"`
	CaptureAutomationCustomTelemetry  bool                 `toml:"captureAutomationCustomTelemetry"`
}

func validateRelayID(id relay.ID) error {
	// only the EVM has specific requirements
	if id.Network == relay.EVM {
		_, err := toml.ChainIDInt64(id.ChainID)
		if err != nil {
			return fmt.Errorf("invalid EVM chain id %s: %w", id.ChainID, err)
		}
	}
	return nil
}

func (s *OCR2OracleSpec) RelayID() (relay.ID, error) {
	cid, err := s.getChainID()
	if err != nil {
		return relay.ID{}, err
	}
	rid := relay.NewID(s.Relay, cid)
	err = validateRelayID(rid)
	if err != nil {
		return relay.ID{}, err
	}
	return rid, nil
}

func (s *OCR2OracleSpec) getChainID() (relay.ChainID, error) {
	if s.ChainID != "" {
		return relay.ChainID(s.ChainID), nil
	}
	// backward compatible job spec
	return s.getChainIdFromRelayConfig()
}

func (s *OCR2OracleSpec) getChainIdFromRelayConfig() (relay.ChainID, error) {

	v, exists := s.RelayConfig["chainID"]
	if !exists {
		return "", fmt.Errorf("chainID does not exist")
	}
	switch t := v.(type) {
	case string:
		return relay.ChainID(t), nil
	case int, int64, int32:
		return relay.ChainID(fmt.Sprintf("%d", v)), nil
	case float64:
		// backward compatibility with JSONConfig.EVMChainID
		i := int64(t)
		return relay.ChainID(strconv.FormatInt(i, 10)), nil

	default:
		return "", fmt.Errorf("unable to parse chainID: unexpected type %T", t)
	}
}

// GetID is a getter function that returns the ID of the spec.
func (s OCR2OracleSpec) GetID() string {
	return fmt.Sprintf("%v", s.ID)
}

// SetID is a setter function that sets the ID of the spec.
func (s *OCR2OracleSpec) SetID(value string) error {
	ID, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	s.ID = int32(ID)
	return nil
}

type ExternalInitiatorWebhookSpec struct {
	ExternalInitiatorID int64
	ExternalInitiator   bridges.ExternalInitiator
	WebhookSpecID       int32
	WebhookSpec         WebhookSpec
	Spec                models.JSON
}

type WebhookSpec struct {
	ID                            int32 `toml:"-"`
	ExternalInitiatorWebhookSpecs []ExternalInitiatorWebhookSpec
	CreatedAt                     time.Time `json:"createdAt" toml:"-"`
	UpdatedAt                     time.Time `json:"updatedAt" toml:"-"`
}

func (w WebhookSpec) GetID() string {
	return fmt.Sprintf("%v", w.ID)
}

func (w *WebhookSpec) SetID(value string) error {
	ID, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	w.ID = int32(ID)
	return nil
}

type DirectRequestSpec struct {
	ID                       int32                    `toml:"-"`
	ContractAddress          ethkey.EIP55Address      `toml:"contractAddress"`
	MinIncomingConfirmations clnull.Uint32            `toml:"minIncomingConfirmations"`
	Requesters               models.AddressCollection `toml:"requesters"`
	MinContractPayment       *commonassets.Link       `toml:"minContractPaymentLinkJuels"`
	EVMChainID               *utils.Big               `toml:"evmChainID"`
	CreatedAt                time.Time                `toml:"-"`
	UpdatedAt                time.Time                `toml:"-"`
}

type CronSpec struct {
	ID           int32     `toml:"-"`
	CronSchedule string    `toml:"schedule"`
	CreatedAt    time.Time `toml:"-"`
	UpdatedAt    time.Time `toml:"-"`
}

func (s CronSpec) GetID() string {
	return fmt.Sprintf("%v", s.ID)
}

func (s *CronSpec) SetID(value string) error {
	ID, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	s.ID = int32(ID)
	return nil
}

type FluxMonitorSpec struct {
	ID              int32               `toml:"-"`
	ContractAddress ethkey.EIP55Address `toml:"contractAddress"`
	Threshold       tomlutils.Float32   `toml:"threshold,float"`
	// AbsoluteThreshold is the maximum absolute change allowed in a fluxmonitored
	// value before a new round should be kicked off, so that the current value
	// can be reported on-chain.
	AbsoluteThreshold   tomlutils.Float32 `toml:"absoluteThreshold,float"`
	PollTimerPeriod     time.Duration
	PollTimerDisabled   bool
	IdleTimerPeriod     time.Duration
	IdleTimerDisabled   bool
	DrumbeatSchedule    string
	DrumbeatRandomDelay time.Duration
	DrumbeatEnabled     bool
	MinPayment          *commonassets.Link
	EVMChainID          *utils.Big `toml:"evmChainID"`
	CreatedAt           time.Time  `toml:"-"`
	UpdatedAt           time.Time  `toml:"-"`
}

type KeeperSpec struct {
	ID                       int32               `toml:"-"`
	ContractAddress          ethkey.EIP55Address `toml:"contractAddress"`
	MinIncomingConfirmations *uint32             `toml:"minIncomingConfirmations"`
	FromAddress              ethkey.EIP55Address `toml:"fromAddress"`
	EVMChainID               *utils.Big          `toml:"evmChainID"`
	CreatedAt                time.Time           `toml:"-"`
	UpdatedAt                time.Time           `toml:"-"`
}

type VRFSpec struct {
	ID int32

	// BatchCoordinatorAddress is the address of the batch vrf coordinator to use.
	// This is required if batchFulfillmentEnabled is set to true in the job spec.
	BatchCoordinatorAddress *ethkey.EIP55Address `toml:"batchCoordinatorAddress"`
	// BatchFulfillmentEnabled indicates to the vrf job to use the batch vrf coordinator
	// for fulfilling requests. If set to true, batchCoordinatorAddress must be set in
	// the job spec.
	BatchFulfillmentEnabled bool `toml:"batchFulfillmentEnabled"`
	// BatchFulfillmentGasMultiplier is used to determine the final gas estimate for the batch
	// fulfillment.
	BatchFulfillmentGasMultiplier tomlutils.Float64 `toml:"batchFulfillmentGasMultiplier"`

	// VRFOwnerAddress is the address of the VRFOwner address to use.
	//
	// V2 only.
	VRFOwnerAddress *ethkey.EIP55Address `toml:"vrfOwnerAddress"`

	CoordinatorAddress       ethkey.EIP55Address   `toml:"coordinatorAddress"`
	PublicKey                secp256k1.PublicKey   `toml:"publicKey"`
	MinIncomingConfirmations uint32                `toml:"minIncomingConfirmations"`
	EVMChainID               *utils.Big            `toml:"evmChainID"`
	FromAddresses            []ethkey.EIP55Address `toml:"fromAddresses"`
	PollPeriod               time.Duration         `toml:"pollPeriod"`          // For v2 jobs
	RequestedConfsDelay      int64                 `toml:"requestedConfsDelay"` // For v2 jobs. Optional, defaults to 0 if not provided.
	RequestTimeout           time.Duration         `toml:"requestTimeout"`      // Optional, defaults to 24hr if not provided.

	// GasLanePrice specifies the gas lane price for this VRF job.
	// If the specified keys in FromAddresses do not have the provided gas price the job
	// will not start.
	//
	// Optional, for v2 jobs only.
	GasLanePrice *assets.Wei `toml:"gasLanePrice" db:"gas_lane_price"`

	// ChunkSize is the number of pending VRF V2 requests to process in parallel. Optional, defaults
	// to 20 if not provided.
	ChunkSize uint32 `toml:"chunkSize"`

	// BackoffInitialDelay is the amount of time to wait before retrying a failed request after the
	// first failure. V2 only.
	BackoffInitialDelay time.Duration `toml:"backoffInitialDelay"`

	// BackoffMaxDelay is the maximum amount of time to wait before retrying a failed request. V2
	// only.
	BackoffMaxDelay time.Duration `toml:"backoffMaxDelay"`

	CreatedAt time.Time `toml:"-"`
	UpdatedAt time.Time `toml:"-"`
}

// BlockhashStoreSpec defines the job spec for the blockhash store feeder.
type BlockhashStoreSpec struct {
	ID int32

	// CoordinatorV1Address is the VRF V1 coordinator to watch for unfulfilled requests. If empty,
	// no V1 coordinator will be watched.
	CoordinatorV1Address *ethkey.EIP55Address `toml:"coordinatorV1Address"`

	// CoordinatorV2Address is the VRF V2 coordinator to watch for unfulfilled requests. If empty,
	// no V2 coordinator will be watched.
	CoordinatorV2Address *ethkey.EIP55Address `toml:"coordinatorV2Address"`

	// CoordinatorV2PlusAddress is the VRF V2Plus coordinator to watch for unfulfilled requests. If empty,
	// no V2Plus coordinator will be watched.
	CoordinatorV2PlusAddress *ethkey.EIP55Address `toml:"coordinatorV2PlusAddress"`

	// LookbackBlocks defines the maximum age of blocks whose hashes should be stored.
	LookbackBlocks int32 `toml:"lookbackBlocks"`

	// WaitBlocks defines the minimum age of blocks whose hashes should be stored.
	WaitBlocks int32 `toml:"waitBlocks"`

	// HeartbeatPeriodTime defines the number of seconds by which we "heartbeat store"
	// a blockhash into the blockhash store contract.
	// This is so that we always have a blockhash to anchor to in the event we need to do a
	// backwards mode on the contract.
	HeartbeatPeriod time.Duration `toml:"heartbeatPeriod"`

	// BlockhashStoreAddress is the address of the BlockhashStore contract to store blockhashes
	// into.
	BlockhashStoreAddress ethkey.EIP55Address `toml:"blockhashStoreAddress"`

	// BatchBlockhashStoreAddress is the address of the trusted BlockhashStore contract to store blockhashes
	TrustedBlockhashStoreAddress *ethkey.EIP55Address `toml:"trustedBlockhashStoreAddress"`

	// BatchBlockhashStoreBatchSize is the number of blockhashes to store in a single batch
	TrustedBlockhashStoreBatchSize int32 `toml:"trustedBlockhashStoreBatchSize"`

	// PollPeriod defines how often recent blocks should be scanned for blockhash storage.
	PollPeriod time.Duration `toml:"pollPeriod"`

	// RunTimeout defines the timeout for a single run of the blockhash store feeder.
	RunTimeout time.Duration `toml:"runTimeout"`

	// EVMChainID defines the chain ID for monitoring and storing of blockhashes.
	EVMChainID *utils.Big `toml:"evmChainID"`

	// FromAddress is the sender address that should be used to store blockhashes.
	FromAddresses []ethkey.EIP55Address `toml:"fromAddresses"`

	// CreatedAt is the time this job was created.
	CreatedAt time.Time `toml:"-"`

	// UpdatedAt is the time this job was last updated.
	UpdatedAt time.Time `toml:"-"`
}

// BlockHeaderFeederSpec defines the job spec for the blockhash store feeder.
type BlockHeaderFeederSpec struct {
	ID int32

	// CoordinatorV1Address is the VRF V1 coordinator to watch for unfulfilled requests. If empty,
	// no V1 coordinator will be watched.
	CoordinatorV1Address *ethkey.EIP55Address `toml:"coordinatorV1Address"`

	// CoordinatorV2Address is the VRF V2 coordinator to watch for unfulfilled requests. If empty,
	// no V2 coordinator will be watched.
	CoordinatorV2Address *ethkey.EIP55Address `toml:"coordinatorV2Address"`

	// CoordinatorV2PlusAddress is the VRF V2Plus coordinator to watch for unfulfilled requests. If empty,
	// no V2Plus coordinator will be watched.
	CoordinatorV2PlusAddress *ethkey.EIP55Address `toml:"coordinatorV2PlusAddress"`

	// LookbackBlocks defines the maximum age of blocks whose hashes should be stored.
	LookbackBlocks int32 `toml:"lookbackBlocks"`

	// WaitBlocks defines the minimum age of blocks whose hashes should be stored.
	WaitBlocks int32 `toml:"waitBlocks"`

	// BlockhashStoreAddress is the address of the BlockhashStore contract to store blockhashes
	// into.
	BlockhashStoreAddress ethkey.EIP55Address `toml:"blockhashStoreAddress"`

	// BatchBlockhashStoreAddress is the address of the BatchBlockhashStore contract to store blockhashes
	// into.
	BatchBlockhashStoreAddress ethkey.EIP55Address `toml:"batchBlockhashStoreAddress"`

	// PollPeriod defines how often recent blocks should be scanned for blockhash storage.
	PollPeriod time.Duration `toml:"pollPeriod"`

	// RunTimeout defines the timeout for a single run of the blockhash store feeder.
	RunTimeout time.Duration `toml:"runTimeout"`

	// EVMChainID defines the chain ID for monitoring and storing of blockhashes.
	EVMChainID *utils.Big `toml:"evmChainID"`

	// FromAddress is the sender address that should be used to store blockhashes.
	FromAddresses []ethkey.EIP55Address `toml:"fromAddresses"`

	// GetBlockHashesBatchSize is the RPC call batch size for retrieving blockhashes
	GetBlockhashesBatchSize uint16 `toml:"getBlockhashesBatchSize"`

	// StoreBlockhashesBatchSize is the RPC call batch size for storing blockhashes
	StoreBlockhashesBatchSize uint16 `toml:"storeBlockhashesBatchSize"`

	// CreatedAt is the time this job was created.
	CreatedAt time.Time `toml:"-"`

	// UpdatedAt is the time this job was last updated.
	UpdatedAt time.Time `toml:"-"`
}

// LegacyGasStationServerSpec defines the job spec for the legacy gas station server.
type LegacyGasStationServerSpec struct {
	ID int32

	// ForwarderAddress is the address of EIP2771 forwarder that verifies signature
	// and forwards requests to target contracts
	ForwarderAddress ethkey.EIP55Address `toml:"forwarderAddress"`

	// EVMChainID defines the chain ID from which the meta-transaction request originates.
	EVMChainID *utils.Big `toml:"evmChainID"`

	// CCIPChainSelector is the CCIP chain selector that corresponds to EVMChainID param.
	// This selector is equivalent to (source) chainID specified in SendTransaction request
	CCIPChainSelector *utils.Big `toml:"ccipChainSelector"`

	// FromAddress is the sender address that should be used to send meta-transactions
	FromAddresses []ethkey.EIP55Address `toml:"fromAddresses"`

	// CreatedAt is the time this job was created.
	CreatedAt time.Time `toml:"-"`

	// UpdatedAt is the time this job was last updated.
	UpdatedAt time.Time `toml:"-"`
}

// LegacyGasStationSidecarSpec defines the job spec for the legacy gas station sidecar.
type LegacyGasStationSidecarSpec struct {
	ID int32

	// ForwarderAddress is the address of EIP2771 forwarder that verifies signature
	// and forwards requests to target contracts
	ForwarderAddress ethkey.EIP55Address `toml:"forwarderAddress"`

	// OffRampAddress is the address of CCIP OffRamp for the given chainID
	OffRampAddress ethkey.EIP55Address `toml:"offRampAddress"`

	// LookbackBlocks defines the maximum number of blocks to search for on-chain events.
	LookbackBlocks int32 `toml:"lookbackBlocks"`

	// PollPeriod defines how frequently legacy gas station sidecar runs.
	PollPeriod time.Duration `toml:"pollPeriod"`

	// RunTimeout defines the timeout for a single run of the legacy gas station sidecar.
	RunTimeout time.Duration `toml:"runTimeout"`

	// EVMChainID defines the chain ID for the on-chain events tracked by sidecar
	EVMChainID *utils.Big `toml:"evmChainID"`

	// CCIPChainSelector is the CCIP chain selector that corresponds to EVMChainID param
	CCIPChainSelector *utils.Big `toml:"ccipChainSelector"`

	// CreatedAt is the time this job was created.
	CreatedAt time.Time `toml:"-"`

	// UpdatedAt is the time this job was last updated.
	UpdatedAt time.Time `toml:"-"`
}

// BootstrapSpec defines the spec to handles the node communication setup process.
type BootstrapSpec struct {
	ID                                int32         `toml:"-"`
	ContractID                        string        `toml:"contractID"`
	FeedID                            *common.Hash  `toml:"feedID"`
	Relay                             relay.Network `toml:"relay"`
	RelayConfig                       JSONConfig
	MonitoringEndpoint                null.String     `toml:"monitoringEndpoint"`
	BlockchainTimeout                 models.Interval `toml:"blockchainTimeout"`
	ContractConfigTrackerPollInterval models.Interval `toml:"contractConfigTrackerPollInterval"`
	ContractConfigConfirmations       uint16          `toml:"contractConfigConfirmations"`
	CreatedAt                         time.Time       `toml:"-"`
	UpdatedAt                         time.Time       `toml:"-"`
}

// AsOCR2Spec transforms the bootstrap spec into a generic OCR2 format to enable code sharing between specs.
func (s BootstrapSpec) AsOCR2Spec() OCR2OracleSpec {
	return OCR2OracleSpec{
		ID:                                s.ID,
		ContractID:                        s.ContractID,
		Relay:                             s.Relay,
		RelayConfig:                       s.RelayConfig,
		MonitoringEndpoint:                s.MonitoringEndpoint,
		BlockchainTimeout:                 s.BlockchainTimeout,
		ContractConfigTrackerPollInterval: s.ContractConfigTrackerPollInterval,
		ContractConfigConfirmations:       s.ContractConfigConfirmations,
		CreatedAt:                         s.CreatedAt,
		UpdatedAt:                         s.UpdatedAt,
		P2PV2Bootstrappers:                pq.StringArray{},
	}
}

type GatewaySpec struct {
	ID            int32      `toml:"-"`
	GatewayConfig JSONConfig `toml:"gatewayConfig"`
	CreatedAt     time.Time  `toml:"-"`
	UpdatedAt     time.Time  `toml:"-"`
}

func (s GatewaySpec) GetID() string {
	return fmt.Sprintf("%v", s.ID)
}

func (s *GatewaySpec) SetID(value string) error {
	ID, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		return err
	}
	s.ID = int32(ID)
	return nil
}

// EALSpec defines the job spec for the gas station.
type EALSpec struct {
	ID int32

	// ForwarderAddress is the address of EIP2771 forwarder that verifies signature
	// and forwards requests to target contracts
	ForwarderAddress ethkey.EIP55Address `toml:"forwarderAddress"`

	// EVMChainID defines the chain ID from which the meta-transaction request originates.
	EVMChainID *utils.Big `toml:"evmChainID"`

	// FromAddress is the sender address that should be used to send meta-transactions
	FromAddresses []ethkey.EIP55Address `toml:"fromAddresses"`

	// LookbackBlocks defines the maximum age of blocks to lookback in status tracker
	LookbackBlocks int32 `toml:"lookbackBlocks"`

	// PollPeriod defines how frequently EAL status tracker runs
	PollPeriod time.Duration `toml:"pollPeriod"`

	// RunTimeout defines the timeout for a single run of EAL status tracker
	RunTimeout time.Duration `toml:"runTimeout"`

	// CreatedAt is the time this job was created.
	CreatedAt time.Time `toml:"-"`

	// UpdatedAt is the time this job was last updated.
	UpdatedAt time.Time `toml:"-"`
}
