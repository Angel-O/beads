package sqlprototype

import (
	"context"
	"fmt"
	"sort"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

// Fork, Checkout, Merge, and DeleteBranch are fixture controls for the A2
// branch contract. They persist named head sets over the same immutable
// revision catalog on both SQL providers. They do not claim that existing bd
// native-branch commands are already wired to this prototype.
func (m *Module) Fork(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return fmt.Errorf("branch name is required")
	}
	ctx := context.Background()
	observation := m.adapter.Publish(ctx, "spike A2: fork memory view", func(session Session) error {
		exists, err := m.repository.viewExists(ctx, session, name)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("branch %q already exists", name)
		}
		return m.repository.copyHeads(ctx, session, m.activeView, name)
	})
	if err := publicationError(observation); err != nil {
		return err
	}
	return nil
}

func (m *Module) Checkout(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "main" && m.activeView == "main" {
		return nil
	}
	var exists bool
	err := m.adapter.Read(context.Background(), func(session Session) error {
		var readErr error
		exists, readErr = m.repository.viewExists(context.Background(), session, name)
		return readErr
	})
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("branch %q not found", name)
	}
	m.activeView = name
	return nil
}

func (m *Module) Merge(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == m.activeView {
		return nil
	}
	ctx := context.Background()
	observation := m.adapter.Publish(ctx, "spike A2: merge memory views", func(session Session) error {
		exists, err := m.repository.viewExists(ctx, session, name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("branch %q not found", name)
		}
		current, err := m.repository.current(ctx, session, m.activeView)
		if err != nil {
			return err
		}
		incoming, err := m.repository.current(ctx, session, name)
		if err != nil {
			return err
		}
		ids := make(map[a2.BeadID]bool, len(current)+len(incoming))
		for id := range current {
			ids[id] = true
		}
		for id := range incoming {
			ids[id] = true
		}
		for beadID := range ids {
			heads, err := m.reduceHeads(ctx, session, beadID, append(append([]a2.RevisionID(nil), current[beadID]...), incoming[beadID]...))
			if err != nil {
				return err
			}
			if err := m.repository.setHeads(ctx, session, m.activeView, beadID, heads); err != nil {
				return err
			}
		}
		return nil
	})
	return publicationError(observation)
}

func (m *Module) DeleteBranch(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == m.activeView {
		return fmt.Errorf("cannot delete active branch %q", name)
	}
	ctx := context.Background()
	observation := m.adapter.Publish(ctx, "spike A2: delete memory view", func(session Session) error {
		exists, err := m.repository.viewExists(ctx, session, name)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("branch %q not found", name)
		}
		return m.repository.deleteView(ctx, session, name)
	})
	if err := publicationError(observation); err != nil {
		return err
	}
	return nil
}

func publicationError(observation Publication) error {
	if observation.State == Published && observation.Err == nil {
		return nil
	}
	if observation.Err != nil {
		return observation.Err
	}
	return fmt.Errorf("branch publication state %d", observation.State)
}

func (m *Module) reduceHeads(ctx context.Context, session Session, beadID a2.BeadID, candidates []a2.RevisionID) ([]a2.RevisionID, error) {
	set := make(map[a2.RevisionID]bool, len(candidates))
	for _, candidate := range candidates {
		if candidate != "" {
			set[candidate] = true
		}
	}
	result := make([]a2.RevisionID, 0, len(set))
	for candidate := range set {
		ancestor := false
		for other := range set {
			if candidate == other {
				continue
			}
			found, err := m.isAncestor(ctx, session, beadID, candidate, other)
			if err != nil {
				return nil, err
			}
			if found {
				ancestor = true
				break
			}
		}
		if !ancestor {
			result = append(result, candidate)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func (m *Module) isAncestor(ctx context.Context, session Session, beadID a2.BeadID, ancestor, descendant a2.RevisionID) (bool, error) {
	seen := make(map[a2.RevisionID]bool)
	stack := []a2.RevisionID{descendant}
	for len(stack) > 0 {
		last := len(stack) - 1
		candidate := stack[last]
		stack = stack[:last]
		if candidate == ancestor {
			return true, nil
		}
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		stored, err := m.repository.revision(ctx, session, beadID, candidate)
		if err != nil {
			return false, err
		}
		stack = append(stack, stored.Revision.Parents...)
	}
	return false, nil
}
