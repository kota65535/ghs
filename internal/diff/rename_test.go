package diff

import "testing"

// labelsIn builds the matches a collection of labels produces, which is what
// PairRenames is handed.
func labelsIn(current, desired map[string]map[string]any) []ElementMatch {
	return MatchElements("labels", current, desired)
}

func TestPairRenamesFoldsTheUnambiguousPair(t *testing.T) {
	current := map[string]map[string]any{
		"bug": element(map[string]any{"id": float64(1), "name": "bug", "color": "d73a4a", "description": "Something is not working"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a", "description": "Something is not working"}),
	}

	folded := PairRenames(labelsIn(current, desired), "name", nil)

	if len(folded) != 1 {
		t.Fatalf("got %d matches, want the delete and the create folded into one: %+v", len(folded), folded)
	}
	match := folded[0]
	if match.Action != ActionUpdate {
		t.Errorf("action = %s, want %s", match.Action, ActionUpdate)
	}
	if match.Name != "defect" {
		t.Errorf("name = %q, want the declared one", match.Name)
	}
	// Update addresses the label as GitHub knows it, so the reported element
	// has to be the one being renamed rather than the declaration.
	if match.Current["name"] != "bug" {
		t.Errorf("current = %+v, want the reported label", match.Current)
	}
	if match.Desired["name"] != "defect" {
		t.Errorf("desired = %+v, want the declaration", match.Desired)
	}
}

func TestPairRenamesComparesOnlyDeclaredFields(t *testing.T) {
	// The file says nothing about the description, so it is not managed and
	// not a reason to call these different labels.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"id": float64(1), "name": "bug", "color": "d73a4a", "description": "Reported by the API"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a"}),
	}

	folded := PairRenames(labelsIn(current, desired), "name", nil)

	if len(folded) != 1 || folded[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want one update", folded)
	}
}

func TestPairRenamesLeavesADifferentLabelAlone(t *testing.T) {
	// A colour the file changes is a label it does not claim is the old one.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "0e8a16"}),
	}

	folded := PairRenames(labelsIn(current, desired), "name", nil)

	if len(folded) != 2 {
		t.Fatalf("got %d matches, want the delete and the create kept apart: %+v", len(folded), folded)
	}
	byName := map[string]Action{}
	for _, match := range folded {
		byName[match.Name] = match.Action
	}
	if byName["bug"] != ActionDelete || byName["defect"] != ActionCreate {
		t.Errorf("got %+v, want bug deleted and defect created", byName)
	}
}

func TestPairRenamesLeavesTheUndecidableAlone(t *testing.T) {
	// Two labels of the same colour deleted, two created: every pairing is as
	// good as the others, so none of them is the one that was meant.
	current := map[string]map[string]any{
		"bug":  element(map[string]any{"name": "bug", "color": "d73a4a"}),
		"task": element(map[string]any{"name": "task", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a"}),
		"chore":  element(map[string]any{"name": "chore", "color": "d73a4a"}),
	}

	folded := PairRenames(labelsIn(current, desired), "name", nil)

	if len(folded) != 4 {
		t.Fatalf("got %d matches, want all four left as they were: %+v", len(folded), folded)
	}
	for _, match := range folded {
		if match.Action == ActionUpdate {
			t.Errorf("%s was folded into an update, which no pairing justifies", match.Name)
		}
	}
}

func TestPairRenamesLeavesTwoCreatesOnOneDeleteAlone(t *testing.T) {
	// One label deleted, two created that equal it: which one it became is not
	// something the file says.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect", "color": "d73a4a"}),
		"fault":  element(map[string]any{"name": "fault", "color": "d73a4a"}),
	}

	folded := PairRenames(labelsIn(current, desired), "name", nil)

	if len(folded) != 3 {
		t.Fatalf("got %d matches, want all three left as they were: %+v", len(folded), folded)
	}
	for _, match := range folded {
		if match.Action == ActionUpdate {
			t.Errorf("%s was folded into an update, which no pairing justifies", match.Name)
		}
	}
}

func TestPairRenamesLeavesOrdinaryChangesAlone(t *testing.T) {
	current := map[string]map[string]any{
		"bug":  element(map[string]any{"name": "bug", "color": "d73a4a"}),
		"gone": element(map[string]any{"name": "gone", "color": "ffffff"}),
	}
	desired := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "0e8a16"}),
		"new": element(map[string]any{"name": "new", "color": "111111"}),
	}

	folded := PairRenames(labelsIn(current, desired), "name", nil)

	if len(folded) != 3 {
		t.Fatalf("got %d matches, want the update, the create and the delete: %+v", len(folded), folded)
	}
	byName := map[string]Action{}
	for _, match := range folded {
		byName[match.Name] = match.Action
	}
	want := map[string]Action{"bug": ActionUpdate, "new": ActionCreate, "gone": ActionDelete}
	for name, action := range want {
		if byName[name] != action {
			t.Errorf("%s = %s, want %s", name, byName[name], action)
		}
	}
}

func TestPairRenamesNeedsSomethingBesidesTheKeyToGoOn(t *testing.T) {
	// A declaration of a name alone equals every reported element apart from
	// its name, so it says nothing about which one it was. Reading it as a
	// rename would turn one label removed and an unrelated one added into a
	// rename of the first.
	current := map[string]map[string]any{
		"bug": element(map[string]any{"name": "bug", "color": "d73a4a"}),
	}
	desired := map[string]map[string]any{
		"defect": element(map[string]any{"name": "defect"}),
	}

	if folded := PairRenames(labelsIn(current, desired), "name", nil); len(folded) != 2 {
		t.Fatalf("got %+v, want the delete and the create kept apart", folded)
	}
}

func TestPairRenamesUsesTheKeyItIsGiven(t *testing.T) {
	// The field an element is matched on is stated per collection, and it is
	// the one a rename changes.
	current := map[string]map[string]any{
		"a": element(map[string]any{"key": "a", "value": "x"}),
	}
	desired := map[string]map[string]any{
		"b": element(map[string]any{"key": "b", "value": "x"}),
	}

	folded := PairRenames(MatchElements("things", current, desired), "key", nil)
	if len(folded) != 1 || folded[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want one update", folded)
	}
}

func TestPairRenamesComparesTheWayAPlanDoes(t *testing.T) {
	// GitHub fills in what a rule leaves out and reports the rules in an order
	// of its own. Neither is a difference in a plan, so neither may stand in
	// the way of a rename.
	current := map[string]map[string]any{
		"protect-main": element(map[string]any{"id": float64(7), "name": "protect-main", "rules": []any{
			map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(1), "dismiss_stale_reviews_on_push": false}},
			map[string]any{"type": "deletion"},
		}}),
	}
	desired := map[string]map[string]any{
		"protect-default": element(map[string]any{"name": "protect-default", "rules": []any{
			map[string]any{"type": "deletion"},
			map[string]any{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": float64(1)}},
		}}),
	}

	folded := PairRenames(MatchElements("rulesets", current, desired), "name", Matches{"rules": {"type"}})
	if len(folded) != 1 || folded[0].Action != ActionUpdate {
		t.Fatalf("got %+v, want one update", folded)
	}
}
