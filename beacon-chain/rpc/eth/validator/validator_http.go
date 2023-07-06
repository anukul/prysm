package validator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/prysmaticlabs/prysm/v4/beacon-chain/core/helpers"
	fieldparams "github.com/prysmaticlabs/prysm/v4/config/fieldparams"
	"github.com/prysmaticlabs/prysm/v4/consensus-types/primitives"
	"github.com/prysmaticlabs/prysm/v4/network"
	"github.com/prysmaticlabs/prysm/v4/time/slots"
)

func (vs *Server) GetAttesterDutiesHTTP(w http.ResponseWriter, r *http.Request) {
	rawRequestedEpoch, err := strconv.ParseInt(mux.Vars(r)["epoch"], 10, 64)
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not decode epoch: " + err.Error(),
			Code:    http.StatusBadRequest,
		})
		return
	}
	requestedEpoch := primitives.Epoch(rawRequestedEpoch)

	var validatorIndices []primitives.ValidatorIndex
	if err = json.NewDecoder(r.Body).Decode(&validatorIndices); err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not decode validators: " + err.Error(),
			Code:    http.StatusBadRequest,
		})
		return
	}

	if vs.SyncChecker.Syncing() {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Beacon node is currently syncing and not serving requests on this endpoint",
			Code:    http.StatusServiceUnavailable,
		})
		return
	}

	cs := vs.TimeFetcher.CurrentSlot()
	currentEpoch := slots.ToEpoch(cs)
	if requestedEpoch > currentEpoch+1 {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: fmt.Sprintf("Request epoch %d cannot be greater than the next epoch %d", requestedEpoch, currentEpoch+1),
			Code:    http.StatusBadRequest,
		})
		return
	}

	isOptimistic, err := vs.OptimisticModeFetcher.IsOptimistic(r.Context())
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not check optimistic status: " + err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	var startSlot primitives.Slot
	if requestedEpoch == currentEpoch+1 {
		startSlot, err = slots.EpochStart(currentEpoch)
	} else {
		startSlot, err = slots.EpochStart(requestedEpoch)
	}
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: fmt.Sprintf("Could not get start slot from epoch %d: %v", requestedEpoch, err),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	s, err := vs.Stater.StateBySlot(r.Context(), startSlot)
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not get state: " + err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	committeeAssignments, _, err := helpers.CommitteeAssignments(r.Context(), s, requestedEpoch)
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not compute committee assignments: " + err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}
	activeValidatorCount, err := helpers.ActiveValidatorCount(r.Context(), s, requestedEpoch)
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not get active validator count: " + err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}
	committeesAtSlot := helpers.SlotCommitteeCount(activeValidatorCount)

	attesterDuties := make([]*AttesterDuty, 0, len(validatorIndices))
	for _, index := range validatorIndices {
		pubkey := s.PubkeyAtIndex(index)
		var zeroPubkey [fieldparams.BLSPubkeyLength]byte
		if bytes.Equal(pubkey[:], zeroPubkey[:]) {
			network.WriteError(w, &network.DefaultErrorJson{
				Message: "Invalid validator index",
				Code:    http.StatusBadRequest,
			})
			return
		}
		committee := committeeAssignments[index]
		if committee == nil {
			continue
		}
		var valIndexInCommittee primitives.CommitteeIndex
		// valIndexInCommittee will be 0 in case we don't get a match. This is a potential false positive,
		// however it's an impossible condition because every validator must be assigned to a committee.
		for cIndex, vIndex := range committee.Committee {
			if vIndex == index {
				valIndexInCommittee = primitives.CommitteeIndex(uint64(cIndex))
				break
			}
		}
		attesterDuties = append(attesterDuties, &AttesterDuty{
			Pubkey:                  pubkey[:],
			ValidatorIndex:          index,
			CommitteeIndex:          committee.CommitteeIndex,
			CommitteeLength:         uint64(len(committee.Committee)),
			CommitteesAtSlot:        committeesAtSlot,
			ValidatorCommitteeIndex: valIndexInCommittee,
			Slot:                    committee.AttesterSlot,
		})
	}

	root, err := attestationDependentRoot(s, requestedEpoch)
	if err != nil {
		network.WriteError(w, &network.DefaultErrorJson{
			Message: "Could not get dependent root: " + err.Error(),
			Code:    http.StatusInternalServerError,
		})
		return
	}

	network.WriteJson(w, &AttesterDutiesResponse{
		Data:                attesterDuties,
		ExecutionOptimistic: isOptimistic,
		DependentRoot:       root,
	})
}
