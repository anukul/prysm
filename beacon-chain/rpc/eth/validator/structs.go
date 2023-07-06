package validator

import "github.com/prysmaticlabs/prysm/v4/consensus-types/primitives"

type AttesterDutiesResponse struct {
	Data                []*AttesterDuty `json:"data"`
	ExecutionOptimistic bool            `json:"execution_optimistic"`
	DependentRoot       []byte          `json:"dependent_root" hex:"true"`
}

type AttesterDuty struct {
	Pubkey                  []byte                    `json:"pubkey" hex:"true"`
	ValidatorIndex          primitives.ValidatorIndex `json:"validator_index"`
	CommitteeIndex          primitives.CommitteeIndex `json:"committee_index"`
	CommitteeLength         uint64                    `json:"committee_length"`
	CommitteesAtSlot        uint64                    `json:"committees_at_slot"`
	ValidatorCommitteeIndex primitives.CommitteeIndex `json:"validator_committee_index"`
	Slot                    primitives.Slot           `json:"slot"`
}
