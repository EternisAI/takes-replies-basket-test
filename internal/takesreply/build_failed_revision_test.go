package takesreply

import "testing"

func TestFailedBuildChoiceIsHiddenOnlyForItsSourceRevision(t *testing.T) {
	draft := Draft{Revision: 7, Choices: []Choice{
		{ID: "route:7", Label: "Keep the original route", ExposureRole: "named_companies"},
		{ID: "build:7", Label: "Build it now", ExposureRole: "build_now"},
	}}
	failed := &Build{ReplyRevision: 7, Status: "FAILED"}
	got := withoutFailedBuildChoice(draft, failed)
	if len(got.Choices) != 1 || got.Choices[0].ID != "route:7" {
		t.Fatalf("failed revision choices = %#v; want the route with Build hidden", got.Choices)
	}

	draft.Revision = 8
	got = withoutFailedBuildChoice(draft, failed)
	if len(got.Choices) != 2 || got.Choices[1].ID != "build:7" {
		t.Fatalf("new revision choices = %#v; prior failure should not hide Build", got.Choices)
	}

	draft.Revision = 7
	for _, status := range []string{"QUEUED", "RUNNING", "COMPLETED", "PARTIAL"} {
		got = withoutFailedBuildChoice(draft, &Build{ReplyRevision: 7, Status: status})
		if len(got.Choices) != 2 {
			t.Fatalf("%s build hid Build choice on its own revision", status)
		}
	}
}
