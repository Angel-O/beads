package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureIsInternallyConsistent(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "internal", "memorybeads", "spikec", "testdata", "succession.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s state
	if err := json.Unmarshal(contents, &s); err != nil {
		t.Fatal(err)
	}
	for _, task := range s.Tasks {
		for _, ref := range task.References {
			m := s.Memories[ref.BeadID]
			if m == nil {
				t.Fatalf("task %s references missing memory %s", task.ID, ref.BeadID)
			}
			if _, ok := findRevision(m, ref.Revision); !ok {
				t.Fatalf("task %s references missing revision %s", task.ID, ref.Revision)
			}
		}
	}
}

func TestRecordedSuccessionRunEvidenceIsInternallyConsistent(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "internal", "memorybeads", "spikec", "testdata", "succession-run-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	var evidence struct {
		Run struct {
			ModelIdentifier              string `json:"model_identifier"`
			VerbatimAgentRepliesRetained bool   `json:"verbatim_agent_replies_retained"`
		} `json:"run"`
		PreEvaluatorEvents []event `json:"pre_evaluator_events"`
		EvaluatorEvents    []event `json:"evaluator_events"`
		FinalState         state   `json:"final_state"`
	}
	if err := json.Unmarshal(contents, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Run.ModelIdentifier != "not retained" || evidence.Run.VerbatimAgentRepliesRetained {
		t.Fatalf("run provenance must describe the retained evidence honestly: %+v", evidence.Run)
	}
	if len(evidence.PreEvaluatorEvents) != 13 {
		t.Fatalf("pre-evaluator events = %d, want 13", len(evidence.PreEvaluatorEvents))
	}
	if first := evidence.PreEvaluatorEvents[0]; first.Command != "show" || first.BeadID != "task-1" {
		t.Fatalf("first agent event = %+v", first)
	}
	if last := evidence.PreEvaluatorEvents[len(evidence.PreEvaluatorEvents)-1]; last.Command != "recall" || last.BeadID != "mem-deploy" || last.Revision != "rev-0031" {
		t.Fatalf("last agent event = %+v", last)
	}
	if len(evidence.EvaluatorEvents) != 1 || evidence.EvaluatorEvents[0].Command != "history" {
		t.Fatalf("evaluator events = %+v", evidence.EvaluatorEvents)
	}
	if len(evidence.FinalState.Memories) != 3 {
		t.Fatalf("final memory count = %d, want 3", len(evidence.FinalState.Memories))
	}
	deployment := evidence.FinalState.Memories["mem-deploy"]
	if deployment == nil || deployment.Current != "rev-0031" || len(deployment.Revisions) != 2 {
		t.Fatalf("final deployment memory = %+v", deployment)
	}
	corrected := deployment.Revisions[1]
	if corrected.Parent != "rev-deploy-1" || corrected.Author != "Fresh Agent <fresh-agent@example.test>" || corrected.Message != "Correct preview deployment region from us-west-1 to us-west-2." {
		t.Fatalf("recorded corrected revision = %+v", corrected)
	}
	storage := evidence.FinalState.Memories["mem-storage"]
	if storage == nil || storage.Current != "rev-storage-1" || len(storage.Revisions) != 1 {
		t.Fatalf("unchanged storage memory = %+v", storage)
	}
}

func TestRememberOnlyReturnsUnchangedForIdenticalCanonicalState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("BEADS_MEMORY_SPIKE_STATE", statePath)
	t.Setenv("BEADS_MEMORY_SPIKE_AUTHOR", "Spike Agent <spike@example.test>")
	s := state{
		ProjectID: "project-test",
		Sequence:  1,
		Memories: map[string]*memory{
			"mem-1": {
				ID: "mem-1", Key: "old-key", Title: "Old title", Lifecycle: "active", Current: "rev-1",
				Revisions: []revision{{ID: "rev-1", Body: "same body", Author: "Original <original@example.test>"}},
			},
		},
		Tasks: map[string]task{},
	}
	if err := saveState(&s); err != nil {
		t.Fatal(err)
	}

	if err := runRemember([]string{"same body", "--id", "mem-1", "--expected-revision", "rev-1", "--key", "new-key", "--title", "New title"}); err != nil {
		t.Fatal(err)
	}
	updated, err := loadState()
	if err != nil {
		t.Fatal(err)
	}
	m := updated.Memories["mem-1"]
	if m.Current == "rev-1" || len(m.Revisions) != 2 || m.Key != "new-key" || m.Title != "New title" {
		t.Fatalf("state-changing request was treated as unchanged: %+v", m)
	}

	if err := runRemember([]string{"same body", "--id", "mem-1", "--expected-revision", m.Current, "--key", "new-key", "--title", "New title"}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := loadState()
	if err != nil {
		t.Fatal(err)
	}
	if got := unchanged.Memories["mem-1"]; got.Current != m.Current || len(got.Revisions) != 2 {
		t.Fatalf("identical request created a revision: %+v", got)
	}
}

// TestSuccessionWorkflowContract keeps the observable command contract used by
// the fresh-agent experiment executable. It does not replay or simulate an
// agent: the recorded run artifact is the evidence of the agent's choices.
// This test makes sure the tool behavior on which those choices depended does
// not quietly drift after the one-off run.
func TestSuccessionWorkflowContract(t *testing.T) {
	fixturePath, err := filepath.Abs(filepath.Join("..", "..", "internal", "memorybeads", "spikec", "testdata", "succession.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixtureContents, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture state
	if err := json.Unmarshal(fixtureContents, &fixture); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	binary := filepath.Join(workspace, "bd")
	build := exec.Command("go", "build", "-o", binary, ".")
	buildOutput, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build spike command: %v\n%s", err, buildOutput)
	}
	runner := spikeCommandRunner{
		t:         t,
		binary:    binary,
		workspace: workspace,
		env: []string{
			"BEADS_MEMORY_SPIKE_FIXTURE=" + fixturePath,
			"BEADS_MEMORY_SPIKE_AUTHOR=Contract Agent <contract-agent@example.test>",
			"BEADS_MEMORY_SPIKE_STATE=" + filepath.Join(workspace, ".beads", stateFileName),
		},
	}

	runner.run("init", "--quiet", "--prefix", "test", "--skip-hooks", "--skip-agents")
	prime := runner.run("prime")
	for _, memory := range fixture.Memories {
		for _, revision := range memory.Revisions {
			if bytes.Contains(prime, []byte(revision.Body)) {
				t.Fatalf("prime injected memory body from %s@%s", memory.ID, revision.ID)
			}
		}
	}
	for _, guidance := range []string{
		"Before `bd remember`, search and recall plausible matches",
		"Keep short-lived progress and scratch state with the task or runtime",
		"deliberately chosen project knowledge",
		"should outlast the current work",
	} {
		if !bytes.Contains(prime, []byte(guidance)) {
			t.Fatalf("prime omitted Memory habit guidance %q", guidance)
		}
	}

	var shown task
	showOutput := runner.run("show", "task-1", "--json")
	decodeCommandJSON(t, showOutput, &shown)
	if len(shown.References) != 1 || shown.References[0].BeadID != "mem-policy" || shown.References[0].Revision != "rev-policy-1" {
		t.Fatalf("task reference = %+v, want mem-policy@rev-policy-1", shown.References)
	}
	policy, ok := findRevision(fixture.Memories["mem-policy"], "rev-policy-1")
	if !ok {
		t.Fatal("fixture policy revision is missing")
	}
	if bytes.Contains(showOutput, []byte(policy.Body)) {
		t.Fatal("task display injected the referenced memory body")
	}

	var recalled struct {
		ID               string `json:"id"`
		SelectedRevision string `json:"selected_revision"`
		Body             string `json:"body"`
		Author           string `json:"author"`
	}
	decodeCommandJSON(t, runner.run("recall", "mem-policy", "--revision", "rev-policy-1", "--json"), &recalled)
	if recalled.ID != "mem-policy" || recalled.SelectedRevision != "rev-policy-1" || recalled.Body != policy.Body {
		t.Fatalf("linked exact recall = %+v", recalled)
	}

	type searchResult struct {
		Items []struct {
			ID              string `json:"id"`
			CurrentRevision string `json:"current_revision"`
			Excerpt         string `json:"excerpt"`
		} `json:"items"`
		Complete bool `json:"complete"`
	}
	var storageSearch searchResult
	decodeCommandJSON(t, runner.run("memories", "storage boundary", "--json", "--details"), &storageSearch)
	if !storageSearch.Complete || len(storageSearch.Items) != 1 || storageSearch.Items[0].ID != "mem-storage" || storageSearch.Items[0].CurrentRevision != "rev-storage-1" {
		t.Fatalf("storage search = %+v", storageSearch)
	}
	storage, ok := findRevision(fixture.Memories["mem-storage"], "rev-storage-1")
	if !ok {
		t.Fatal("fixture storage revision is missing")
	}
	if storageSearch.Items[0].Excerpt == storage.Body {
		t.Fatal("compact search returned the complete storage body")
	}
	decodeCommandJSON(t, runner.run("recall", "mem-storage", "--revision", "rev-storage-1", "--json"), &recalled)
	if recalled.ID != "mem-storage" || recalled.SelectedRevision != "rev-storage-1" || recalled.Body != storage.Body {
		t.Fatalf("unlinked exact recall = %+v", recalled)
	}

	var absent searchResult
	decodeCommandJSON(t, runner.run("memories", "catering vendor", "--json", "--details"), &absent)
	if !absent.Complete || len(absent.Items) != 0 {
		t.Fatalf("absent search = %+v", absent)
	}

	var unchanged struct {
		Outcome  string `json:"outcome"`
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	decodeCommandJSON(t, runner.run(
		"remember", storage.Body,
		"--id", "mem-storage", "--expected-revision", "rev-storage-1", "--json",
	), &unchanged)
	if unchanged.Outcome != "unchanged" || unchanged.ID != "mem-storage" || unchanged.Revision != "rev-storage-1" {
		t.Fatalf("unchanged result = %+v", unchanged)
	}

	statePath := filepath.Join(workspace, ".beads", stateFileName)
	beforeConflict, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	conflictOutput := runner.runFailure(
		"remember", "conflicting storage guidance",
		"--id", "mem-storage", "--expected-revision", "rev-stale", "--json",
	)
	if !strings.Contains(string(conflictOutput), "stale revision") {
		t.Fatalf("stale write error = %q", conflictOutput)
	}
	afterConflict, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeConflict, afterConflict) {
		t.Fatal("stale write mutated the workspace")
	}

	var phraseMiss, previewHit searchResult
	decodeCommandJSON(t, runner.run("memories", "preview deployment region", "--json", "--details"), &phraseMiss)
	if !phraseMiss.Complete || len(phraseMiss.Items) != 0 {
		t.Fatalf("expected recorded phrase-search friction, got %+v", phraseMiss)
	}
	decodeCommandJSON(t, runner.run("memories", "preview", "--json", "--details"), &previewHit)
	if !previewHit.Complete || len(previewHit.Items) != 1 || previewHit.Items[0].ID != "mem-deploy" {
		t.Fatalf("preview recovery search = %+v", previewHit)
	}
	decodeCommandJSON(t, runner.run("recall", "mem-deploy", "--revision", "rev-deploy-1", "--json"), &recalled)
	if recalled.Body != "The preview service is deployed in us-west-1." {
		t.Fatalf("stale deployment body = %q", recalled.Body)
	}

	message := "Correct preview deployment region from us-west-1 to us-west-2."
	var applied struct {
		Outcome  string `json:"outcome"`
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	decodeCommandJSON(t, runner.run(
		"remember", "The preview service is deployed in us-west-2.",
		"--id", "mem-deploy", "--expected-revision", "rev-deploy-1",
		"--message", message, "--json",
	), &applied)
	if applied.Outcome != "applied" || applied.ID != "mem-deploy" || applied.Revision != "rev-0031" {
		t.Fatalf("applied result = %+v", applied)
	}
	decodeCommandJSON(t, runner.run("recall", "mem-deploy", "--revision", applied.Revision, "--json"), &recalled)
	if recalled.SelectedRevision != "rev-0031" || recalled.Body != "The preview service is deployed in us-west-2." || recalled.Author != "Contract Agent <contract-agent@example.test>" {
		t.Fatalf("corrected exact recall = %+v", recalled)
	}

	var storageHistory, deploymentHistory struct {
		ID        string     `json:"id"`
		Revisions []revision `json:"revisions"`
		Complete  bool       `json:"complete"`
	}
	decodeCommandJSON(t, runner.run("history", "mem-storage", "--json"), &storageHistory)
	if !storageHistory.Complete || len(storageHistory.Revisions) != 1 || storageHistory.Revisions[0].ID != "rev-storage-1" {
		t.Fatalf("unchanged write altered storage history: %+v", storageHistory)
	}
	decodeCommandJSON(t, runner.run("history", "mem-deploy", "--json"), &deploymentHistory)
	if !deploymentHistory.Complete || len(deploymentHistory.Revisions) != 2 {
		t.Fatalf("deployment history = %+v", deploymentHistory)
	}
	corrected := deploymentHistory.Revisions[1]
	if corrected.ID != "rev-0031" || corrected.Parent != "rev-deploy-1" || corrected.Author != "Contract Agent <contract-agent@example.test>" || corrected.Message != message {
		t.Fatalf("corrected revision provenance = %+v", corrected)
	}

	var events []event
	decodeCommandJSON(t, runner.run("events"), &events)
	if len(events) == 0 || events[0].Command != "show" || events[len(events)-1].Command != "history" {
		t.Fatalf("workflow events = %+v", events)
	}
	finalContents, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var final state
	if err := json.Unmarshal(finalContents, &final); err != nil {
		t.Fatal(err)
	}
	if len(final.Memories) != len(fixture.Memories) {
		t.Fatalf("memory count = %d, want %d; workflow created a duplicate", len(final.Memories), len(fixture.Memories))
	}
}

type spikeCommandRunner struct {
	t         *testing.T
	binary    string
	workspace string
	env       []string
}

func (r spikeCommandRunner) run(args ...string) []byte {
	r.t.Helper()
	output, err := r.command(args...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("%s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return output
}

func (r spikeCommandRunner) runFailure(args ...string) []byte {
	r.t.Helper()
	output, err := r.command(args...).CombinedOutput()
	if err == nil {
		r.t.Fatalf("%s unexpectedly succeeded\n%s", strings.Join(args, " "), output)
	}
	return output
}

func (r spikeCommandRunner) command(args ...string) *exec.Cmd {
	command := exec.Command(r.binary, args...)
	command.Dir = r.workspace
	command.Env = overrideEnvironment(os.Environ(), r.env)
	return command
}

func overrideEnvironment(base, overrides []string) []string {
	replaced := make(map[string]bool, len(overrides))
	for _, item := range overrides {
		key, _, _ := strings.Cut(item, "=")
		replaced[key] = true
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if !replaced[key] {
			result = append(result, item)
		}
	}
	return append(result, overrides...)
}

func decodeCommandJSON(t *testing.T, output []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(output, target); err != nil {
		t.Fatalf("decode command JSON: %v\n%s", err, output)
	}
}
