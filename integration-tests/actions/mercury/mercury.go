package mercury

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/lib/pq"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	cl_env_config "github.com/smartcontractkit/chainlink-env/config"
	"github.com/smartcontractkit/chainlink-env/environment"
	"github.com/smartcontractkit/chainlink-env/pkg/helm/chainlink"
	eth "github.com/smartcontractkit/chainlink-env/pkg/helm/ethereum"
	mshelm "github.com/smartcontractkit/chainlink-env/pkg/helm/mercury-server"
	"github.com/smartcontractkit/chainlink-env/pkg/helm/mockserver"
	mockservercfg "github.com/smartcontractkit/chainlink-env/pkg/helm/mockserver-cfg"
	relaymercury "github.com/smartcontractkit/chainlink-relay/pkg/reportingplugins/mercury"
	"github.com/smartcontractkit/chainlink-testing-framework/blockchain"
	ctfClient "github.com/smartcontractkit/chainlink-testing-framework/client"
	"github.com/smartcontractkit/chainlink-testing-framework/utils"
	"github.com/smartcontractkit/chainlink/core/services/job"
	"github.com/smartcontractkit/chainlink/core/services/keystore/chaintype"
	"github.com/smartcontractkit/chainlink/core/store/models"
	networks "github.com/smartcontractkit/chainlink/integration-tests"
	"github.com/smartcontractkit/chainlink/integration-tests/actions"
	mercuryserveraction "github.com/smartcontractkit/chainlink/integration-tests/actions/mercury/mercuryserver"
	"github.com/smartcontractkit/chainlink/integration-tests/client"
	"github.com/smartcontractkit/chainlink/integration-tests/config"
	"github.com/smartcontractkit/chainlink/integration-tests/contracts"
	"github.com/smartcontractkit/libocr/offchainreporting2/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	"gopkg.in/guregu/null.v4"
)

type MercuryTestEnv struct {
	TestEnv             *environment.Environment
	ChainlinkNodes      []*client.Chainlink
	MercuryServer       *client.MercuryServer
	MSAdminId           string
	MSAdminKey          string
	MSAdminEncryptedKey string
	// MSRemoteUrl           string
	IsExistingTestEnv     bool
	EvmClient             blockchain.EVMClient
	VerifierContract      contracts.Verifier
	VerifierProxyContract contracts.VerifierProxy
}

func (t *MercuryTestEnv) JsonConfig() {

}

// Setup DON, Mercury Server and all mandatory contracts
func SetupFullMercuryEnv(t *testing.T, dbSettings map[string]interface{}, serverResources map[string]interface{}) *MercuryTestEnv {
	testNetwork := networks.SelectedNetwork
	evmConfig := eth.New(nil)
	if !testNetwork.Simulated {
		evmConfig = eth.New(&eth.Props{
			NetworkName: testNetwork.Name,
			Simulated:   testNetwork.Simulated,
			WsURLs:      testNetwork.URLs,
		})
	}

	isExistingTestEnv := os.Getenv(cl_env_config.EnvVarNamespace) != "" && os.Getenv(cl_env_config.EnvVarNoManifestUpdate) == "true"

	testEnv := SetupDON(t, testNetwork, evmConfig)

	msAdminId := os.Getenv("MS_DATABASE_FIRST_ADMIN_ID")
	msAdminKey := os.Getenv("MS_DATABASE_FIRST_ADMIN_KEY")
	msAdminEncryptedKey := os.Getenv("MS_DATABASE_FIRST_ADMIN_ENCRYPTED_KEY")
	msRpcPubKey := mercuryserveraction.SetupMercuryServer(t, testEnv, dbSettings, serverResources,
		msAdminId, msAdminEncryptedKey)

	chainlinkNodes, err := client.ConnectChainlinkNodes(testEnv)
	require.NoError(t, err, "Error connecting to Chainlink nodes")

	evmClient, err := blockchain.NewEVMClient(testNetwork, testEnv)
	require.NoError(t, err, "Error connecting to blockchain")

	// Setup random mock server response for mercury price feed
	mockserverClient, err := ctfClient.ConnectMockServer(testEnv)
	require.NoError(t, err, "Error connecting to mock server")

	// mercuryServerLocalUrl := testEnvironment.URLs[mercury_server.URLsKey][0]
	msLocalUrl := testEnv.URLs[mshelm.URLsKey][1]
	msClient := client.NewMercuryServer(msLocalUrl)

	var verifier contracts.Verifier
	var verifierProxy contracts.VerifierProxy

	if isExistingTestEnv {
		// TODO: Connect to existing contracts. Pass their address via env var
	} else {
		var feedId = StringToByte32("ETH-USD-1")

		nodesWithoutBootstrap := chainlinkNodes[1:]
		ocrConfig := BuildMercuryOCRConfig(t, nodesWithoutBootstrap)
		verifier, verifierProxy, _, _ = SetupMercuryContracts(t, evmClient,
			msLocalUrl, feedId, ocrConfig)
		_ = verifierProxy

		latestBlockNum, err := evmClient.LatestBlockNumber(context.Background())
		require.NoError(t, err)

		msRemoteUrl := testEnv.URLs[mshelm.URLsKey][0]
		SetupMercuryNodeJobs(t, chainlinkNodes, mockserverClient, verifier.Address(),
			feedId, latestBlockNum, msRemoteUrl, msRpcPubKey, testNetwork.ChainID, 0)

		verifier.SetConfig(feedId, ocrConfig)

		// Wait for the DON to start generating reports
		d := 160 * time.Second
		log.Info().Msgf("Sleeping for %s to wait for Mercury env to be ready..", d)
		time.Sleep(d)
	}

	t.Cleanup(func() {
		if isExistingTestEnv {
			log.Info().Msg("Do not tear down existing environment")
		} else {
			err := actions.TeardownSuite(t, testEnv, utils.ProjectRoot, chainlinkNodes, nil, zapcore.PanicLevel, evmClient)
			require.NoError(t, err, "Error tearing down environment")
		}
	})

	mercuryTestEnv := &MercuryTestEnv{
		TestEnv:               testEnv,
		ChainlinkNodes:        chainlinkNodes,
		MercuryServer:         msClient,
		MSAdminId:             msAdminId,
		MSAdminKey:            msAdminKey,
		MSAdminEncryptedKey:   msAdminEncryptedKey,
		IsExistingTestEnv:     isExistingTestEnv,
		EvmClient:             evmClient,
		VerifierContract:      verifier,
		VerifierProxyContract: verifierProxy,
	}

	// TODO: Log test env as json so that it can be saved to env variable and used to re-connect to it
	// log.Info().Msgf("%s", mercuryTestEnv.JsonConfig())

	return mercuryTestEnv
}

func SetupDON(t *testing.T, evmNetwork blockchain.EVMNetwork, evmConfig environment.ConnectedChart) *environment.Environment {
	testEnv := environment.New(&environment.Config{
		// TODO: comment
		TTL:             1 * time.Hour,
		NamespacePrefix: fmt.Sprintf("smoke-mercury-%s", strings.ReplaceAll(strings.ToLower(evmNetwork.Name), " ", "-")),
		Test:            t,
	}).
		AddHelm(mockservercfg.New(nil)).
		AddHelm(mockserver.New(map[string]interface{}{
			"app": map[string]interface{}{
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"cpu":    "4000m",
						"memory": "4048Mi",
					},
					"limits": map[string]interface{}{
						"cpu":    "4000m",
						"memory": "4048Mi",
					},
				},
			},
		})).
		AddHelm(evmConfig).
		AddHelm(chainlink.New(0, map[string]interface{}{
			"replicas": "5",
			"toml": client.AddNetworksConfig(
				config.BaseMercuryTomlConfig,
				evmNetwork),
			// "secretsToml": secretsToml,
			"prometheus": "true",
		}))
	err := testEnv.Run()
	require.NoError(t, err, "Error running test environment")

	return testEnv
}

func SetupMercuryNodeJobs(
	t *testing.T,
	chainlinkNodes []*client.Chainlink,
	mockserverClient *ctfClient.MockserverClient,
	contractID string,
	feedId [32]byte,
	fromBlock uint64,
	msRemoteUrl string,
	mercuryServerPubKey ed25519.PublicKey,
	chainID int64,
	keyIndex int,
) {
	err := mockserverClient.SetRandomValuePath("/variable")
	require.NoError(t, err, "Setting mockserver value path shouldn't fail")

	observationSource := fmt.Sprintf(`
// Benchmark Price
price1          [type=http method=GET url="%[1]s" allowunrestrictednetworkaccess="true"];
price1_parse    [type=jsonparse path="data,result"];
price1_multiply [type=multiply times=100000000 index=0];

price1 -> price1_parse -> price1_multiply;

// Bid
bid          [type=http method=GET url="%[1]s" allowunrestrictednetworkaccess="true"];
bid_parse    [type=jsonparse path="data,result"];
bid_multiply [type=multiply times=100000000 index=1];

bid -> bid_parse -> bid_multiply;

// Ask
ask          [type=http method=GET url="%[1]s" allowunrestrictednetworkaccess="true"];
ask_parse    [type=jsonparse path="data,result"];
ask_multiply [type=multiply times=100000000 index=2];

ask -> ask_parse -> ask_multiply;	

// Block Num + Hash
b1                 [type=ethgetblock];
bnum_lookup        [type=lookup key="number" index=3];
bhash_lookup       [type=lookup key="hash" index=4];

b1 -> bnum_lookup;
b1 -> bhash_lookup;`, mockserverClient.Config.ClusterURL+"/variable")

	bootstrapNode := chainlinkNodes[0]
	bootstrapNode.RemoteIP()
	bootstrapP2PIds, err := bootstrapNode.MustReadP2PKeys()
	require.NoError(t, err, "Shouldn't fail reading P2P keys from bootstrap node")
	bootstrapP2PId := bootstrapP2PIds.Data[0].Attributes.PeerID

	bootstrapSpec := &client.OCR2TaskJobSpec{
		Name:    "ocr2 bootstrap node",
		JobType: "bootstrap",
		OCR2OracleSpec: job.OCR2OracleSpec{
			ContractID: contractID,
			Relay:      "evm",
			RelayConfig: map[string]interface{}{
				"chainID": int(chainID),
				"feedID":  fmt.Sprintf("\"0x%x\"", feedId),
			},
			ContractConfigTrackerPollInterval: *models.NewInterval(time.Second * 15),
		},
	}
	_, err = bootstrapNode.MustCreateJob(bootstrapSpec)
	require.NoError(t, err, "Shouldn't fail creating bootstrap job on bootstrap node")
	P2Pv2Bootstrapper := fmt.Sprintf("%s@%s:%d", bootstrapP2PId, bootstrapNode.RemoteIP(), 6690)

	for nodeIndex := 1; nodeIndex < len(chainlinkNodes); nodeIndex++ {
		nodeOCRKeys, err := chainlinkNodes[nodeIndex].MustReadOCR2Keys()
		require.NoError(t, err, "Shouldn't fail getting OCR keys from OCR node %d", nodeIndex+1)
		csaKeys, _, err := chainlinkNodes[nodeIndex].ReadCSAKeys()
		require.NoError(t, err)
		// csaKeyId := csaKeys.Data[0].ID
		csaPubKey := csaKeys.Data[0].Attributes.PublicKey

		var nodeOCRKeyId []string
		for _, key := range nodeOCRKeys.Data {
			if key.Attributes.ChainType == string(chaintype.EVM) {
				nodeOCRKeyId = append(nodeOCRKeyId, key.ID)
				break
			}
		}

		jobSpec := client.OCR2TaskJobSpec{
			Name:            "ocr2",
			JobType:         "offchainreporting2",
			MaxTaskDuration: "1s",
			OCR2OracleSpec: job.OCR2OracleSpec{
				PluginType: "mercury",
				// PluginConfig: map[string]interface{}{
				// 	"juelsPerFeeCoinSource": `"""
				// 		bn1          [type=ethgetblock];
				// 		bn1_lookup   [type=lookup key="number"];
				// 		bn1 -> bn1_lookup;
				// 	"""`,
				// },
				// TODO: Fix local mercury server url. Should be only host without port. Or, local wsrpc url
				PluginConfig: map[string]interface{}{
					// "serverHost":   fmt.Sprintf("\"%s:1338\"", mercury_server.URLsKey),
					"serverURL":    fmt.Sprintf("\"%s:1338\"", msRemoteUrl[7:len(msRemoteUrl)-5]),
					"serverPubKey": fmt.Sprintf("\"%s\"", hex.EncodeToString(mercuryServerPubKey)),
				},
				Relay: "evm",
				RelayConfig: map[string]interface{}{
					"chainID":   int(chainID),
					"feedID":    fmt.Sprintf("\"0x%x\"", feedId),
					"fromBlock": fromBlock,
				},
				// RelayConfigMercuryConfig: map[string]interface{}{
				// 	"clientPrivKeyID": csaKeyId,
				// },
				ContractConfigTrackerPollInterval: *models.NewInterval(time.Second * 15),
				ContractID:                        contractID,
				OCRKeyBundleID:                    null.StringFrom(nodeOCRKeyId[keyIndex]),
				TransmitterID:                     null.StringFrom(csaPubKey),
				P2PV2Bootstrappers:                pq.StringArray{P2Pv2Bootstrapper},
			},
			ObservationSource: observationSource,
		}

		_, err = chainlinkNodes[nodeIndex].MustCreateJob(&jobSpec)
		require.NoError(t, err, "Shouldn't fail creating OCR Task job on OCR node %d", nodeIndex+1)
	}
	log.Info().Msg("Done creating OCR automation jobs")
}

func BuildMercuryOCRConfig(
	t *testing.T,
	chainlinkNodes []*client.Chainlink,
) contracts.MercuryOCRConfig {
	// Build onchain config
	c := relaymercury.OnchainConfig{Min: big.NewInt(0), Max: big.NewInt(math.MaxInt64)}
	onchainConfig, err := (relaymercury.StandardOnchainConfigCodec{}).Encode(c)
	require.NoError(t, err, "Shouldn't fail encoding config")

	_, oracleIdentities := getOracleIdentities(t, chainlinkNodes)
	signerOnchainPublicKeys, _, f, onchainConfig,
		offchainConfigVersion, offchainConfig, err := confighelper.ContractSetConfigArgsForTests(
		2*time.Second,        // deltaProgress time.Duration,
		20*time.Second,       // deltaResend time.Duration,
		100*time.Millisecond, // deltaRound time.Duration,
		0,                    // deltaGrace time.Duration,
		1*time.Minute,        // deltaStage time.Duration,
		100,                  // rMax uint8,
		[]int{len(chainlinkNodes)},
		oracleIdentities,
		[]byte{},
		0*time.Millisecond, // maxDurationQuery time.Duration,
		// TODO: fix mockserver and switch to 250ms?
		500*time.Millisecond, // maxDurationObservation time.Duration,
		500*time.Millisecond, // maxDurationReport time.Duration,
		500*time.Millisecond, // maxDurationShouldAcceptFinalizedReport time.Duration,
		500*time.Millisecond, // maxDurationShouldTransmitAcceptedReport time.Duration,
		// 250*time.Millisecond, // maxDurationObservation time.Duration,
		// 250*time.Millisecond, // maxDurationReport time.Duration,
		// 250*time.Millisecond, // maxDurationShouldAcceptFinalizedReport time.Duration,
		// 250*time.Millisecond, // maxDurationShouldTransmitAcceptedReport time.Duration,
		1, // f int,
		onchainConfig,
	)
	require.NoError(t, err)

	// Convert signers to addresses
	var signers []common.Address
	for _, signer := range signerOnchainPublicKeys {
		require.Equal(t, 20, len(signer), "OnChainPublicKey has wrong length for address")
		signers = append(signers, common.BytesToAddress(signer))
	}

	// Use node CSA pub key as transmitter
	transmitters := make([][32]byte, len(chainlinkNodes))
	for i, n := range chainlinkNodes {
		csaKeys, _, err := n.ReadCSAKeys()
		require.NoError(t, err)
		csaPubKey, err := hex.DecodeString(csaKeys.Data[0].Attributes.PublicKey)
		require.NoError(t, err)
		transmitters[i] = [32]byte(csaPubKey)
	}

	return contracts.MercuryOCRConfig{
		Signers:               signers,
		Transmitters:          transmitters,
		F:                     f,
		OnchainConfig:         onchainConfig,
		OffchainConfigVersion: offchainConfigVersion,
		OffchainConfig:        offchainConfig,
	}
}

func getOracleIdentities(t *testing.T, chainlinkNodes []*client.Chainlink) ([]int, []confighelper.OracleIdentityExtra) {
	l := zerolog.New(zerolog.NewTestWriter(t))
	S := make([]int, len(chainlinkNodes))
	oracleIdentities := make([]confighelper.OracleIdentityExtra, len(chainlinkNodes))
	sharedSecretEncryptionPublicKeys := make([]types.ConfigEncryptionPublicKey, len(chainlinkNodes))
	var wg sync.WaitGroup
	for i, cl := range chainlinkNodes {
		wg.Add(1)
		go func(i int, cl *client.Chainlink) {
			defer wg.Done()

			address, err := cl.PrimaryEthAddress()
			_ = address
			require.NoError(t, err, "Shouldn't fail getting primary ETH address from OCR node: index %d", i)
			ocr2Keys, err := cl.MustReadOCR2Keys()
			require.NoError(t, err, "Shouldn't fail reading OCR2 keys from node")
			var ocr2Config client.OCR2KeyAttributes
			for _, key := range ocr2Keys.Data {
				if key.Attributes.ChainType == string(chaintype.EVM) {
					ocr2Config = key.Attributes
					break
				}
			}

			keys, err := cl.MustReadP2PKeys()
			require.NoError(t, err, "Shouldn't fail reading P2P keys from node")
			p2pKeyID := keys.Data[0].Attributes.PeerID

			offchainPkBytes, err := hex.DecodeString(strings.TrimPrefix(ocr2Config.OffChainPublicKey, "ocr2off_evm_"))
			require.NoError(t, err, "failed to decode %s: %v", ocr2Config.OffChainPublicKey, err)

			offchainPkBytesFixed := [ed25519.PublicKeySize]byte{}
			n := copy(offchainPkBytesFixed[:], offchainPkBytes)
			require.Equal(t, ed25519.PublicKeySize, n, "Wrong number of elements copied")

			configPkBytes, err := hex.DecodeString(strings.TrimPrefix(ocr2Config.ConfigPublicKey, "ocr2cfg_evm_"))
			require.NoError(t, err, "failed to decode %s: %v", ocr2Config.ConfigPublicKey, err)

			configPkBytesFixed := [ed25519.PublicKeySize]byte{}
			n = copy(configPkBytesFixed[:], configPkBytes)
			require.Equal(t, ed25519.PublicKeySize, n, "Wrong number of elements copied")

			onchainPkBytes, err := hex.DecodeString(strings.TrimPrefix(ocr2Config.OnChainPublicKey, "ocr2on_evm_"))
			require.NoError(t, err, "failed to decode %s: %v", ocr2Config.OnChainPublicKey, err)

			csaKeys, _, err := cl.ReadCSAKeys()
			require.NoError(t, err)

			sharedSecretEncryptionPublicKeys[i] = configPkBytesFixed
			oracleIdentities[i] = confighelper.OracleIdentityExtra{
				OracleIdentity: confighelper.OracleIdentity{
					OnchainPublicKey:  onchainPkBytes,
					OffchainPublicKey: offchainPkBytesFixed,
					PeerID:            p2pKeyID,
					TransmitAccount:   types.Account(csaKeys.Data[0].Attributes.PublicKey),
				},
				ConfigEncryptionPublicKey: configPkBytesFixed,
			}
			S[i] = 1
		}(i, cl)
	}
	wg.Wait()
	l.Info().Msg("Done fetching oracle identities")
	return S, oracleIdentities
}

func StringToByte32(str string) [32]byte {
	var bytes [32]byte
	copy(bytes[:], str)
	return bytes
}
