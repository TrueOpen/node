package types

import (
	"fmt"
	"sort"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// ValidateParamsVersionGenesis validates primary parameter bodies and overrides;
// private counts and indexes are derived rather than accepted from the caller.
func ValidateParamsVersionGenesis(genesis GenesisState) error {
	current := genesis.ParamsMeta.ParamsVersion
	history := make(map[uint64]TaskParamsHistoryState, len(genesis.ParamsHistory))
	var versions []uint64
	for _, row := range genesis.ParamsHistory {
		if row.ParamsVersion >= current {
			return fmt.Errorf("parameter history must precede current version")
		}
		if _, exists := history[row.ParamsVersion]; exists {
			return fmt.Errorf("duplicate parameter history version")
		}
		if row.UpdatedHeight > row.SupersededHeight || row.SupersededHeight > genesis.ParamsMeta.UpdatedHeight || row.SupersededHeight != 0 && !shared.IsBlockHeightV1(row.SupersededHeight) {
			return fmt.Errorf("parameter history has invalid effective heights")
		}
		if err := row.Params.Validate(); err != nil {
			return fmt.Errorf("parameter history version %d: %w", row.ParamsVersion, err)
		}
		history[row.ParamsVersion] = row
		versions = append(versions, row.ParamsVersion)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	for i := 1; i < len(versions); i++ {
		previous, next := history[versions[i-1]], history[versions[i]]
		if previous.SupersededHeight > next.UpdatedHeight {
			return fmt.Errorf("parameter history intervals overlap")
		}
		if versions[i-1]+1 == versions[i] && previous.SupersededHeight != next.UpdatedHeight {
			return fmt.Errorf("consecutive parameter history heights disagree")
		}
	}
	if len(versions) > 0 {
		last := history[versions[len(versions)-1]]
		if last.ParamsVersion+1 == current && last.SupersededHeight != genesis.ParamsMeta.UpdatedHeight {
			return fmt.Errorf("parameter history does not meet current metadata")
		}
	}
	tasks, sessions := make(map[string]bool, len(genesis.TaskCores)), make(map[string]bool, len(genesis.Streams))
	for _, row := range genesis.TaskCores {
		tasks[string(row.TaskId)] = true
	}
	for _, row := range genesis.Streams {
		sessions[string(row.SessionId)] = true
	}
	validate := func(rows []ParamsVersionBindingState, objects map[string]bool) error {
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			key := string(row.ObjectId)
			if len(row.ObjectId) != Hash32Len || !objects[key] || seen[key] {
				return fmt.Errorf("parameter version binding has a duplicate or unknown object")
			}
			if row.ParamsVersion == current {
				return fmt.Errorf("current parameter version overrides are redundant")
			}
			if _, exists := history[row.ParamsVersion]; !exists {
				return fmt.Errorf("parameter version binding has no retained historical body")
			}
			seen[key] = true
		}
		return nil
	}
	if err := validate(genesis.TaskParamsVersionBindings, tasks); err != nil {
		return err
	}
	return validate(genesis.SessionParamsVersionBindings, sessions)
}
