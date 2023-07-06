package validator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	mockChain "github.com/prysmaticlabs/prysm/v4/beacon-chain/blockchain/testing"
	"github.com/prysmaticlabs/prysm/v4/beacon-chain/core/transition"
	dbutil "github.com/prysmaticlabs/prysm/v4/beacon-chain/db/testing"
	"github.com/prysmaticlabs/prysm/v4/beacon-chain/rpc/testutil"
	"github.com/prysmaticlabs/prysm/v4/beacon-chain/state"
	mockSync "github.com/prysmaticlabs/prysm/v4/beacon-chain/sync/initial-sync/testing"
	fieldparams "github.com/prysmaticlabs/prysm/v4/config/fieldparams"
	"github.com/prysmaticlabs/prysm/v4/config/params"
	"github.com/prysmaticlabs/prysm/v4/consensus-types/primitives"
	"github.com/prysmaticlabs/prysm/v4/network"
	"github.com/prysmaticlabs/prysm/v4/testing/assert"
	"github.com/prysmaticlabs/prysm/v4/testing/require"
	"github.com/prysmaticlabs/prysm/v4/testing/util"
	"github.com/prysmaticlabs/prysm/v4/time/slots"
)

func TestGetAttesterDutiesHTTP(t *testing.T) {
	ctx := context.Background()
	genesis := util.NewBeaconBlock()
	depChainStart := params.BeaconConfig().MinGenesisActiveValidatorCount
	deposits, _, err := util.DeterministicDepositsAndKeys(depChainStart)
	require.NoError(t, err)
	eth1Data, err := util.DeterministicEth1Data(len(deposits))
	require.NoError(t, err)
	bs, err := transition.GenesisBeaconState(context.Background(), deposits, 0, eth1Data)
	require.NoError(t, err, "Could not set up genesis state")
	// Set state to non-epoch start slot.
	require.NoError(t, bs.SetSlot(5))
	genesisRoot, err := genesis.Block.HashTreeRoot()
	require.NoError(t, err, "Could not get signing root")
	roots := make([][]byte, fieldparams.BlockRootsLength)
	roots[0] = genesisRoot[:]
	require.NoError(t, bs.SetBlockRoots(roots))
	db := dbutil.SetupDB(t)

	chainSlot := primitives.Slot(0)
	chain := &mockChain.ChainService{
		State: bs, Root: genesisRoot[:], Slot: &chainSlot,
	}

	// Deactivate last validator.
	vals := bs.Validators()
	vals[len(vals)-1].ExitEpoch = 0
	require.NoError(t, bs.SetValidators(vals))

	pubKeys := make([][]byte, len(deposits))
	for i := 0; i < len(deposits); i++ {
		pubKeys[i] = deposits[i].Data.PublicKey
	}

	// nextEpochState must not be used for committee calculations when requesting next epoch
	nextEpochState := bs.Copy()
	require.NoError(t, nextEpochState.SetSlot(params.BeaconConfig().SlotsPerEpoch))
	require.NoError(t, nextEpochState.SetValidators(vals[:512]))

	vs := &Server{
		Stater: &testutil.MockStater{
			StatesBySlot: map[primitives.Slot]state.BeaconState{
				0:                                   bs,
				params.BeaconConfig().SlotsPerEpoch: nextEpochState,
			},
		},

		TimeFetcher:           chain,
		SyncChecker:           &mockSync.Sync{IsSyncing: false},
		OptimisticModeFetcher: chain,
	}

	t.Run("Single validator", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{0})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(0, 10),
		})
		writer := httptest.NewRecorder()

		// handler
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		resp := &AttesterDutiesResponse{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), resp))
		assert.DeepEqual(t, genesisRoot[:], resp.DependentRoot)
		require.Equal(t, 1, len(resp.Data))
		duty := resp.Data[0]
		assert.Equal(t, primitives.CommitteeIndex(1), duty.CommitteeIndex)
		assert.Equal(t, primitives.Slot(0), duty.Slot)
		assert.Equal(t, primitives.ValidatorIndex(0), duty.ValidatorIndex)
		assert.DeepEqual(t, pubKeys[0], duty.Pubkey)
		assert.Equal(t, uint64(171), duty.CommitteeLength)
		assert.Equal(t, uint64(3), duty.CommitteesAtSlot)
		assert.Equal(t, primitives.CommitteeIndex(80), duty.ValidatorCommitteeIndex)
	})

	t.Run("Multiple validators", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{0, 1})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(0, 10),
		})
		writer := httptest.NewRecorder()

		// handler
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		resp := &AttesterDutiesResponse{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), resp))
		assert.Equal(t, 2, len(resp.Data))
	})

	t.Run("Next epoch", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{0})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(int64(slots.ToEpoch(bs.Slot())+1), 10),
		})
		writer := httptest.NewRecorder()

		// handler
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		resp := &AttesterDutiesResponse{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), resp))
		assert.DeepEqual(t, genesisRoot[:], resp.DependentRoot)
		require.Equal(t, 1, len(resp.Data))
		duty := resp.Data[0]
		assert.Equal(t, primitives.CommitteeIndex(0), duty.CommitteeIndex)
		assert.Equal(t, primitives.Slot(62), duty.Slot)
		assert.Equal(t, primitives.ValidatorIndex(0), duty.ValidatorIndex)
		assert.DeepEqual(t, pubKeys[0], duty.Pubkey)
		assert.Equal(t, uint64(170), duty.CommitteeLength)
		assert.Equal(t, uint64(3), duty.CommitteesAtSlot)
		assert.Equal(t, primitives.CommitteeIndex(110), duty.ValidatorCommitteeIndex)
	})

	t.Run("Epoch out of bound", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{0})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		currentEpoch := slots.ToEpoch(bs.Slot())
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(int64(currentEpoch.Add(2)), 10),
		})
		writer := httptest.NewRecorder()

		// handler
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		assert.Equal(t, http.StatusBadRequest, writer.Code)
		e := &network.DefaultErrorJson{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), e))
		assert.Equal(t, http.StatusBadRequest, e.Code)
		assert.Equal(t, fmt.Sprintf("Request epoch %d cannot be greater than the next epoch %d", currentEpoch+2, currentEpoch+1), e.Message)
	})

	t.Run("Validator index out of bound", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{primitives.ValidatorIndex(len(pubKeys))})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(0, 10),
		})
		writer := httptest.NewRecorder()

		// handler
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		assert.Equal(t, http.StatusBadRequest, writer.Code)
		e := &network.DefaultErrorJson{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), e))
		assert.Equal(t, http.StatusBadRequest, e.Code)
		assert.Equal(t, "Invalid validator index", e.Message)
	})

	t.Run("Inactive validator - no duties", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{primitives.ValidatorIndex(len(pubKeys) - 1)})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(0, 10),
		})
		writer := httptest.NewRecorder()

		// handler
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		resp := &AttesterDutiesResponse{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), resp))
		assert.Equal(t, 0, len(resp.Data))
	})

	t.Run("execution optimistic", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{0})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(0, 10),
		})

		// handler
		parentRoot := [32]byte{'a'}
		blk := util.NewBeaconBlock()
		blk.Block.ParentRoot = parentRoot[:]
		blk.Block.Slot = 31
		root, err := blk.Block.HashTreeRoot()
		require.NoError(t, err)
		util.SaveBlock(t, ctx, db, blk)
		require.NoError(t, db.SaveGenesisBlockRoot(ctx, root))

		chainSlot := primitives.Slot(0)
		chain := &mockChain.ChainService{
			State: bs, Root: genesisRoot[:], Slot: &chainSlot, Optimistic: true,
		}
		vs := &Server{
			Stater:                &testutil.MockStater{StatesBySlot: map[primitives.Slot]state.BeaconState{0: bs}},
			TimeFetcher:           chain,
			OptimisticModeFetcher: chain,
			SyncChecker:           &mockSync.Sync{IsSyncing: false},
		}
		writer := httptest.NewRecorder()
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		resp := &AttesterDutiesResponse{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), resp))
		assert.Equal(t, true, resp.ExecutionOptimistic)
	})

	t.Run("sync not ready", func(t *testing.T) {
		// request body
		validatorIndices, err := json.Marshal([]primitives.ValidatorIndex{0})
		require.NoError(t, err)
		var body bytes.Buffer
		_, err = body.Write(validatorIndices)
		require.NoError(t, err)

		request := httptest.NewRequest("POST", "/eth/v1/validator/duties/attester/{epoch}", &body)
		// request path params
		request = mux.SetURLVars(request, map[string]string{
			"epoch": strconv.FormatInt(0, 10),
		})

		// handler
		vs.SyncChecker = &mockSync.Sync{IsSyncing: true}
		writer := httptest.NewRecorder()
		vs.GetAttesterDutiesHTTP(writer, request)

		// response assertions
		e := &network.DefaultErrorJson{}
		require.NoError(t, json.Unmarshal(writer.Body.Bytes(), e))
		assert.Equal(t, http.StatusServiceUnavailable, e.Code)
		assert.Equal(t, "Beacon node is currently syncing and not serving requests on this endpoint", e.Message)
	})
}
