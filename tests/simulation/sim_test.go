package simulation

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"runtime/debug"
	"strings"
	"testing"

	dbm "github.com/cometbft/cometbft-db"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/log"
	tmrand "github.com/cometbft/cometbft/libs/rand"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/baseapp"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	simtypes "github.com/cosmos/cosmos-sdk/types/simulation"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	authzkeeper "github.com/cosmos/cosmos-sdk/x/authz/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	capabilitytypes "github.com/cosmos/cosmos-sdk/x/capability/types"
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	evidencetypes "github.com/cosmos/cosmos-sdk/x/evidence/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	paramtypes "github.com/cosmos/cosmos-sdk/x/params/types"
	"github.com/cosmos/cosmos-sdk/x/simulation"
	simcli "github.com/cosmos/cosmos-sdk/x/simulation/client/cli"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v7/modules/apps/transfer/types"
	ibcexported "github.com/cosmos/ibc-go/v7/modules/core/exported"
	"gotest.tools/v3/assert"

	umeeapp "github.com/umee-network/umee/v6/app"
	appparams "github.com/umee-network/umee/v6/app/params"
	leveragetypes "github.com/umee-network/umee/v6/x/leverage/types"
	oracletypes "github.com/umee-network/umee/v6/x/oracle/types"
)

func init() {
	simcli.GetSimulatorFlags()
}

// TestFullAppSimulation tests application fuzzing given a random seed as input.
func TestFullAppSimulation(t *testing.T) {
	config := simcli.NewConfigFromFlags()
	db, dir, logger, skip, err := simtestutil.SetupSimulation(config, "leveldb-app-sim", "Simulation", false, true)
	if skip {
		t.Skip("skipping application simulation")
	}

	assert.NilError(t, err, "simulation setup failed")

	defer func() {
		db.Close()
		assert.NilError(t, os.RemoveAll(dir))
	}()

	app := umeeapp.New(
		logger,
		db,
		nil,
		true,
		map[int64]bool{},
		umeeapp.DefaultNodeHome,
		simcli.FlagPeriodValue,
		umeeapp.EmptyAppOptions{},
		umeeapp.EmptyWasmOpts,
		fauxMerkleModeOpt,
	)
	assert.Equal(t, appparams.Name, app.Name())

	// run randomized simulation
	_, simParams, simErr := simulation.SimulateFromSeed(
		t,
		os.Stdout,
		app.BaseApp,
		appStateFn(app.AppCodec(), app.StateSimulationManager),
		simtypes.RandomAccounts,
		simtestutil.SimulationOperations(app, app.AppCodec(), config),
		app.ModuleAccountAddrs(),
		config,
		app.AppCodec(),
	)

	// export state and simParams before the simulation error is checked
	err = simtestutil.CheckExportSimulation(app, config, simParams)
	assert.NilError(t, err)
	assert.NilError(t, simErr)

	if config.Commit {
		simtestutil.PrintStats(db)
	}
}

// TestAppStateDeterminism tests for application non-determinism using a PRNG
// as an input for the simulator's seed.
func TestAppStateDeterminism(t *testing.T) {
	if !simcli.FlagEnabledValue {
		t.Skip("skipping application simulation")
	}

	config := simcli.NewConfigFromFlags()
	config.InitialBlockHeight = 1
	config.ExportParamsPath = ""
	config.OnOperation = false
	config.AllInvariants = false
	config.ChainID = fmt.Sprintf("simulation-chain-%s", tmrand.NewRand().Str(6))

	numSeeds := 3
	numTimesToRunPerSeed := 5
	appHashList := make([]json.RawMessage, numTimesToRunPerSeed)

	for i := 0; i < numSeeds; i++ {
		config.Seed = rand.Int63()

		for j := 0; j < numTimesToRunPerSeed; j++ {
			var logger log.Logger
			if simcli.FlagVerboseValue {
				logger = log.TestingLogger()
			} else {
				logger = log.NewNopLogger()
			}

			db := dbm.NewMemDB()
			app := umeeapp.New(
				logger,
				db,
				nil,
				true,
				map[int64]bool{},
				umeeapp.DefaultNodeHome,
				simcli.FlagPeriodValue,
				umeeapp.EmptyAppOptions{},
				umeeapp.EmptyWasmOpts,
				interBlockCacheOpt(),
				baseapp.SetChainID(config.ChainID),
			)

			fmt.Printf(
				"running non-determinism simulation; seed %d; run: %d/%d; attempt: %d/%d\n",
				config.Seed, i+1, numSeeds, j+1, numTimesToRunPerSeed,
			)

			_, _, err := simulation.SimulateFromSeed(
				t,
				os.Stdout,
				app.BaseApp,
				appStateFn(app.AppCodec(), app.StateSimulationManager),
				simtypes.RandomAccounts,
				simtestutil.SimulationOperations(app, app.AppCodec(), config),
				app.ModuleAccountAddrs(),
				config,
				app.AppCodec(),
			)
			assert.NilError(t, err)

			if config.Commit {
				simtestutil.PrintStats(db)
			}

			appHash := app.LastCommitID().Hash
			appHashList[j] = appHash

			if j != 0 {
				assert.Equal(
					t,
					string(appHashList[0]),
					string(appHashList[j]),
					"non-determinism in seed %d; run: %d/%d; attempt: %d/%d\n",
					config.Seed, i+1, numSeeds, j+1, numTimesToRunPerSeed,
				)
			}
		}
	}
}

func BenchmarkFullAppSimulation(b *testing.B) {
	config := simcli.NewConfigFromFlags()
	db, dir, logger, skip, err := simtestutil.SetupSimulation(config, "leveldb-app-bench-sim", "Simulation", false, true)
	if skip {
		b.Skip("skipping application simulation")
	}

	assert.NilError(b, err, "simulation setup failed")

	defer func() {
		db.Close()
		assert.NilError(b, os.RemoveAll(dir))
	}()

	app := umeeapp.New(
		logger,
		db,
		nil,
		true,
		map[int64]bool{},
		umeeapp.DefaultNodeHome,
		simcli.FlagPeriodValue,
		umeeapp.EmptyAppOptions{},
		umeeapp.EmptyWasmOpts,
		interBlockCacheOpt(),
	)

	// Run randomized simulation:w
	_, simParams, simErr := simulation.SimulateFromSeed(
		b,
		os.Stdout,
		app.BaseApp,
		appStateFn(app.AppCodec(), app.StateSimulationManager),
		simtypes.RandomAccounts,
		simtestutil.SimulationOperations(app, app.AppCodec(), config),
		app.ModuleAccountAddrs(),
		config,
		app.AppCodec(),
	)

	// export state and simParams before the simulation error is checked
	err = simtestutil.CheckExportSimulation(app, config, simParams)
	assert.NilError(b, err)
	assert.NilError(b, simErr)

	if config.Commit {
		simtestutil.PrintStats(db)
	}
}

func TestAppImportExport(t *testing.T) {
	db, dir, app, logger, exported, stopEarly, newDB, newDir, newApp, _ := appExportAndImport(t)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Sprintf("%v", r)
			if !strings.Contains(err, "validator set is empty after InitGenesis") {
				panic(r)
			}
			logger.Info("Skipping simulation as all validators have been unbonded")
			logger.Info("err", err, "stacktrace", string(debug.Stack()))
		}
	}()

	defer func() {
		defer func() {
			db.Close()
			assert.NilError(t, os.RemoveAll(dir))
		}()
	}()

	defer func() {
		assert.NilError(t, newDB.Close())
		assert.NilError(t, os.RemoveAll(newDir))
	}()

	if stopEarly {
		fmt.Println("can't export or import a zero-validator genesis, exiting test...")
		return
	}

	ctxA := app.NewContext(true, tmproto.Header{Height: app.LastBlockHeight()})
	ctxB := newApp.NewContext(true, tmproto.Header{Height: app.LastBlockHeight()})

	newApp.InitChainer(ctxB, abci.RequestInitChain{
		AppStateBytes:   exported.AppState,
		ConsensusParams: exported.ConsensusParams,
	})
	newApp.StoreConsensusParams(ctxB, exported.ConsensusParams)

	fmt.Printf("comparing stores...\n")

	storeKeysPrefixes := []StoreKeysPrefixes{
		{app.GetKey(authtypes.StoreKey), newApp.GetKey(authtypes.StoreKey), [][]byte{}},
		{
			app.GetKey(stakingtypes.StoreKey), newApp.GetKey(stakingtypes.StoreKey),
			[][]byte{
				stakingtypes.UnbondingQueueKey, stakingtypes.RedelegationQueueKey, stakingtypes.ValidatorQueueKey,
				stakingtypes.HistoricalInfoKey, stakingtypes.UnbondingIDKey, stakingtypes.UnbondingIndexKey, stakingtypes.UnbondingTypeKey, stakingtypes.ValidatorUpdatesKey,
			},
		}, // ordering may change but it doesn't matter
		{app.GetKey(slashingtypes.StoreKey), newApp.GetKey(slashingtypes.StoreKey), [][]byte{}},
		{app.GetKey(minttypes.StoreKey), newApp.GetKey(minttypes.StoreKey), [][]byte{}},
		{app.GetKey(distrtypes.StoreKey), newApp.GetKey(distrtypes.StoreKey), [][]byte{}},
		{app.GetKey(banktypes.StoreKey), newApp.GetKey(banktypes.StoreKey), [][]byte{banktypes.BalancesPrefix}},
		{app.GetKey(paramtypes.StoreKey), newApp.GetKey(paramtypes.StoreKey), [][]byte{}},
		{app.GetKey(govtypes.StoreKey), newApp.GetKey(govtypes.StoreKey), [][]byte{}},
		{app.GetKey(evidencetypes.StoreKey), newApp.GetKey(evidencetypes.StoreKey), [][]byte{}},
		{app.GetKey(capabilitytypes.StoreKey), newApp.GetKey(capabilitytypes.StoreKey), [][]byte{}},
		{app.GetKey(authzkeeper.StoreKey), newApp.GetKey(authzkeeper.StoreKey), [][]byte{authzkeeper.GrantKey, authzkeeper.GrantQueuePrefix}},

		{app.GetKey(ibcexported.StoreKey), newApp.GetKey(ibcexported.StoreKey), [][]byte{}},
		{app.GetKey(ibctransfertypes.StoreKey), newApp.GetKey(ibctransfertypes.StoreKey), [][]byte{}},

		// Umee module
		{app.GetKey(leveragetypes.StoreKey), newApp.GetKey(leveragetypes.StoreKey), [][]byte{}},
		{app.GetKey(oracletypes.StoreKey), newApp.GetKey(oracletypes.StoreKey), [][]byte{}},
	}

	for _, skp := range storeKeysPrefixes {
		storeA := ctxA.KVStore(skp.A)
		storeB := ctxB.KVStore(skp.B)

		failedKVAs, failedKVBs := sdk.DiffKVStores(storeA, storeB, skp.Prefixes)
		assert.Equal(t, len(failedKVAs), len(failedKVBs), "unequal sets of key-values to compare")

		fmt.Printf("compared %d different key/value pairs between %s and %s\n", len(failedKVAs), skp.A, skp.B)

		assert.Equal(t, 0, len(failedKVAs), simtestutil.GetSimulationLog(skp.A.Name(), app.SimulationManager().StoreDecoders, failedKVAs, failedKVBs))
	}
}

func TestAppSimulationAfterImport(t *testing.T) {
	db, dir, app, logger, exported, stopEarly, newDB, newDir, newApp, config := appExportAndImport(t)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Sprintf("%v", r)
			if !strings.Contains(err, "validator set is empty after InitGenesis") {
				panic(r)
			}
			logger.Info("Skipping simulation as all validators have been unbonded")
			logger.Info("err", err, "stacktrace", string(debug.Stack()))
		}
	}()

	defer func() {
		db.Close()
		assert.NilError(t, os.RemoveAll(dir))
	}()

	defer func() {
		assert.NilError(t, newDB.Close())
		assert.NilError(t, os.RemoveAll(newDir))
	}()

	if stopEarly {
		fmt.Println("can't export or import a zero-validator genesis, exiting test...")
		return
	}

	// importing the old app genesis into new app
	ctxB := newApp.NewContext(true, tmproto.Header{Height: app.LastBlockHeight()})
	newApp.InitChainer(ctxB, abci.RequestInitChain{
		AppStateBytes:   exported.AppState,
		ConsensusParams: exported.ConsensusParams,
	})
	newApp.StoreConsensusParams(ctxB, exported.ConsensusParams)

	_, _, err := simulation.SimulateFromSeed(
		t,
		os.Stdout,
		newApp.BaseApp,
		appStateFn(newApp.AppCodec(), newApp.StateSimulationManager),
		simtypes.RandomAccounts, // Replace with own random account function if using keys other than secp256k1
		simtestutil.SimulationOperations(newApp, newApp.AppCodec(), config),
		newApp.ModuleAccountAddrs(),
		config,
		newApp.AppCodec(),
	)
	assert.NilError(t, err)
}
