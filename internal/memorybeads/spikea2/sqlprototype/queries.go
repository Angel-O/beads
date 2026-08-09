package sqlprototype

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	a2 "github.com/steveyegge/beads/internal/memorybeads/spikea2"
)

func (m *Module) History(ctx context.Context, request a2.HistoryRequest) (a2.HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return a2.HistoryPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	signature := m.cursorSignature("history|" + string(request.BeadID) + "|" + strconv.Itoa(request.Limit))
	if request.Continuation != "" {
		record, err := m.continuation(request.Continuation, "history", signature)
		if err != nil {
			return a2.HistoryPage{}, err
		}
		return m.historyPage(record, request.Limit), nil
	}

	var stored []storedRevision
	err := m.adapter.Read(ctx, func(session Session) error {
		var readErr error
		stored, readErr = m.repository.revisions(ctx, session, request.BeadID)
		return readErr
	})
	if err != nil {
		return a2.HistoryPage{}, err
	}
	if len(stored) == 0 {
		return a2.HistoryPage{}, fmt.Errorf("%w: bead %q", a2.ErrNotFound, request.BeadID)
	}
	// The identifier order is provider-private but deterministic. Pages retain
	// this snapshot rather than re-running the query.
	sort.Slice(stored, func(i, j int) bool {
		return stored[i].Revision.Address.RevisionID > stored[j].Revision.Address.RevisionID
	})
	items := make([]a2.RevisionSummary, 0, len(stored))
	for _, entry := range stored {
		revision := entry.Revision
		items = append(items, a2.RevisionSummary{
			Address:        revision.Address,
			Parents:        append([]a2.RevisionID(nil), revision.Parents...),
			Key:            revision.Key,
			Title:          revision.Title,
			Lifecycle:      revision.Lifecycle,
			Author:         revision.Author,
			AssistingAgent: revision.AssistingAgent,
			ChangeMessage:  revision.ChangeMessage,
			Origin:         revision.Origin,
			Provenance:     cloneProvenance(revision.Provenance),
			CreatedAt:      revision.CreatedAt,
		})
	}
	return m.historyPage(cursorRecord{kind: "history", signature: signature, history: items}, request.Limit), nil
}

func (m *Module) historyPage(record cursorRecord, limit int) a2.HistoryPage {
	start, end := pageBounds(record.offset, limit, len(record.history))
	page := a2.HistoryPage{Revisions: cloneRevisionSummaries(record.history[start:end]), Complete: end == len(record.history)}
	if !page.Complete {
		record.offset = end
		page.Continuation = m.saveCursor(record)
	}
	return page
}

func (m *Module) Search(ctx context.Context, request a2.SearchRequest) (a2.SearchPage, error) {
	if err := ctx.Err(); err != nil {
		return a2.SearchPage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	signature := m.cursorSignature("search|" + strings.ToLower(request.Query) + "|" + strconv.FormatBool(request.IncludeArchived) + "|" + strconv.Itoa(request.Limit))
	if request.Continuation != "" {
		record, err := m.continuation(request.Continuation, "search", signature)
		if err != nil {
			return a2.SearchPage{}, err
		}
		return m.searchPage(record, request.Limit), nil
	}

	query := strings.ToLower(request.Query)
	items := make([]a2.SearchSummary, 0)
	err := m.adapter.Read(ctx, func(session Session) error {
		current, err := m.repository.current(ctx, session, m.activeView)
		if err != nil {
			return err
		}
		beadIDs := make([]a2.BeadID, 0, len(current))
		for beadID := range current {
			beadIDs = append(beadIDs, beadID)
		}
		sort.Slice(beadIDs, func(i, j int) bool { return beadIDs[i] < beadIDs[j] })
		for _, beadID := range beadIDs {
			heads := current[beadID]
			if len(heads) > 1 {
				return newConflictError(beadID, heads)
			}
			if len(heads) == 0 {
				continue
			}
			stored, err := m.repository.revision(ctx, session, beadID, heads[0])
			if err != nil {
				return err
			}
			revision := stored.Revision
			if revision.Lifecycle == a2.LifecycleArchived && !request.IncludeArchived {
				continue
			}
			if query != "" && !strings.Contains(searchableText(revision), query) {
				continue
			}
			items = append(items, a2.SearchSummary{
				ProjectID:       m.projectID,
				BeadID:          beadID,
				CurrentRevision: revision.Address.RevisionID,
				Key:             revision.Key,
				Title:           revision.Title,
				Lifecycle:       revision.Lifecycle,
				Excerpt:         excerpt(revision.Body, 48),
			})
		}
		return nil
	})
	if err != nil {
		return a2.SearchPage{}, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].BeadID < items[j].BeadID })
	return m.searchPage(cursorRecord{kind: "search", signature: signature, search: items}, request.Limit), nil
}

func (m *Module) searchPage(record cursorRecord, limit int) a2.SearchPage {
	start, end := pageBounds(record.offset, limit, len(record.search))
	page := a2.SearchPage{Memories: append([]a2.SearchSummary(nil), record.search[start:end]...), Complete: end == len(record.search)}
	if !page.Complete {
		record.offset = end
		page.Continuation = m.saveCursor(record)
	}
	return page
}

func (m *Module) References(ctx context.Context, request a2.ReferencesRequest) (a2.ReferencePage, error) {
	if err := ctx.Err(); err != nil {
		return a2.ReferencePage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	signature := m.cursorSignature("refs|" + string(request.BeadID) + "|" + string(request.RevisionID) + "|" + strconv.Itoa(request.Limit))
	if request.Continuation != "" {
		record, err := m.continuation(request.Continuation, "refs", signature)
		if err != nil {
			return a2.ReferencePage{}, err
		}
		return m.referencePage(record, request.Limit), nil
	}
	if request.RevisionID == "" {
		return a2.ReferencePage{}, fmt.Errorf("%w: outgoing reference traversal requires an exact source revision", a2.ErrInvalid)
	}
	var stored *storedRevision
	err := m.adapter.Read(ctx, func(session Session) error {
		var readErr error
		stored, readErr = m.repository.selectRevision(ctx, session, m.activeView, request.BeadID, request.RevisionID)
		return readErr
	})
	if err != nil {
		return a2.ReferencePage{}, err
	}
	items := cloneReferences(stored.Revision.References)
	return m.referencePage(cursorRecord{kind: "refs", signature: signature, refs: items}, request.Limit), nil
}

func (m *Module) referencePage(record cursorRecord, limit int) a2.ReferencePage {
	start, end := pageBounds(record.offset, limit, len(record.refs))
	page := a2.ReferencePage{References: cloneReferences(record.refs[start:end]), Complete: end == len(record.refs)}
	if !page.Complete {
		record.offset = end
		page.Continuation = m.saveCursor(record)
	}
	return page
}

func (m *Module) Diff(ctx context.Context, request a2.DiffRequest) (a2.DiffResult, error) {
	if err := ctx.Err(); err != nil {
		return a2.DiffResult{}, err
	}
	if request.From == "" || request.To == "" {
		return a2.DiffResult{}, fmt.Errorf("%w: diff requires two exact revision IDs", a2.ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var from, to *storedRevision
	err := m.adapter.Read(ctx, func(session Session) error {
		var err error
		from, err = m.repository.selectRevision(ctx, session, m.activeView, request.BeadID, request.From)
		if err != nil {
			return err
		}
		to, err = m.repository.selectRevision(ctx, session, m.activeView, request.BeadID, request.To)
		return err
	})
	if err != nil {
		return a2.DiffResult{}, err
	}
	result := a2.DiffResult{From: from.Revision.Address, To: to.Revision.Address}
	appendFieldChange(&result, "key", from.Revision.Key, to.Revision.Key)
	appendFieldChange(&result, "aliases", append([]string(nil), from.Revision.Aliases...), append([]string(nil), to.Revision.Aliases...))
	appendFieldChange(&result, "title", from.Revision.Title, to.Revision.Title)
	appendFieldChange(&result, "body", from.Revision.Body, to.Revision.Body)
	appendFieldChange(&result, "lifecycle", from.Revision.Lifecycle, to.Revision.Lifecycle)
	appendFieldChange(&result, "author", from.Revision.Author, to.Revision.Author)
	appendFieldChange(&result, "assisting_agent", from.Revision.AssistingAgent, to.Revision.AssistingAgent)
	appendFieldChange(&result, "change_message", from.Revision.ChangeMessage, to.Revision.ChangeMessage)
	appendFieldChange(&result, "origin", from.Revision.Origin, to.Revision.Origin)
	appendFieldChange(&result, "provenance", cloneProvenance(from.Revision.Provenance), cloneProvenance(to.Revision.Provenance))
	result.ReferencesAdded, result.ReferencesRemoved = referenceSetDiff(from.Revision.References, to.Revision.References)
	return result, nil
}

func appendFieldChange(result *a2.DiffResult, field string, before, after any) {
	if !reflect.DeepEqual(before, after) {
		result.Fields = append(result.Fields, a2.FieldChange{Field: field, Before: before, After: after})
	}
}

func (m *Module) Blame(ctx context.Context, request a2.BlameRequest) (a2.BlameResult, error) {
	if err := ctx.Err(); err != nil {
		return a2.BlameResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var stored *storedRevision
	err := m.adapter.Read(ctx, func(session Session) error {
		var readErr error
		stored, readErr = m.repository.selectRevision(ctx, session, m.activeView, request.BeadID, request.RevisionID)
		return readErr
	})
	if err != nil {
		return a2.BlameResult{}, err
	}
	lines := bodyLines(stored.Revision.Body)
	result := a2.BlameResult{Address: stored.Revision.Address, Lines: make([]a2.LineAttribution, len(lines))}
	for i := range lines {
		result.Lines[i] = a2.LineAttribution{Line: lines[i], RevisionID: stored.LineBlame[i]}
	}
	for _, field := range semanticFields() {
		result.Fields = append(result.Fields, a2.FieldAttribution{Field: field, RevisionID: stored.FieldBlame[field]})
	}
	return result, nil
}

func (m *Module) saveCursor(record cursorRecord) a2.Continuation {
	m.cursorSeq++
	token := a2.Continuation(m.instance + "-continuation-" + strconv.FormatUint(m.cursorSeq, 10))
	m.cursors[token] = record
	return token
}

func (m *Module) cursorSignature(request string) string {
	return m.instance + "|" + string(m.projectID) + "|" + m.activeView + "|" + request
}

func (m *Module) continuation(token a2.Continuation, kind, signature string) (cursorRecord, error) {
	record, ok := m.cursors[token]
	if !ok || record.kind != kind || record.signature != signature {
		return cursorRecord{}, a2.ErrInvalidContinuation
	}
	return record, nil
}

func pageBounds(offset, limit, length int) (int, int) {
	if offset > length {
		offset = length
	}
	if limit <= 0 || offset+limit > length {
		return offset, length
	}
	return offset, offset + limit
}

func cloneRevisionSummaries(summaries []a2.RevisionSummary) []a2.RevisionSummary {
	result := append([]a2.RevisionSummary(nil), summaries...)
	for i := range result {
		result[i].Parents = append([]a2.RevisionID(nil), result[i].Parents...)
		result[i].Provenance = cloneProvenance(result[i].Provenance)
	}
	return result
}

func searchableText(revision a2.Revision) string {
	return strings.ToLower(strings.Join([]string{
		revision.Key,
		strings.Join(revision.Aliases, "\n"),
		revision.Title,
		revision.Body,
	}, "\n"))
}

func excerpt(body string, maxRunes int) string {
	runes := []rune(body)
	if len(runes) <= maxRunes {
		return body
	}
	return string(runes[:maxRunes])
}

func referenceSetDiff(from, to []a2.Reference) (added, removed []a2.Reference) {
	fromSet := make(map[string]a2.Reference, len(from))
	toSet := make(map[string]a2.Reference, len(to))
	for _, ref := range from {
		fromSet[referenceKey(ref)] = ref
	}
	for _, ref := range to {
		toSet[referenceKey(ref)] = ref
	}
	for key, ref := range toSet {
		if _, ok := fromSet[key]; !ok {
			added = append(added, ref)
		}
	}
	for key, ref := range fromSet {
		if _, ok := toSet[key]; !ok {
			removed = append(removed, ref)
		}
	}
	sort.Slice(added, func(i, j int) bool { return referenceKey(added[i]) < referenceKey(added[j]) })
	sort.Slice(removed, func(i, j int) bool { return referenceKey(removed[i]) < referenceKey(removed[j]) })
	return added, removed
}
