package docs

import (
	"strings"
	"testing"
)

func TestTopics(t *testing.T) {
	topics := Topics()
	if len(topics) == 0 {
		t.Fatal("expected some topics")
	}
	for _, topic := range topics {
		if topic.Name == "" || topic.Title == "" || topic.Content == "" {
			t.Fatalf("topic has empty fields: %+v", topic)
		}
	}
}

func TestGet(t *testing.T) {
	topic := Get("architecture")
	if topic == nil {
		t.Fatal("expected architecture topic")
	}
	if topic.Title != "Architecture" {
		t.Fatalf("unexpected title: %s", topic.Title)
	}
}

func TestGetMissing(t *testing.T) {
	if Get("nonexistent") != nil {
		t.Fatal("expected nil for unknown topic")
	}
}

func TestListTopics(t *testing.T) {
	list := ListTopics()
	if !strings.Contains(list, "architecture") {
		t.Fatalf("expected 'architecture' in list: %s", list)
	}
	if !strings.Contains(list, "quickstart") {
		t.Fatalf("expected 'quickstart' in list: %s", list)
	}
}

// TestSafetyTopic pins the sync-safeguards topic and the commands and
// settings it has to name, so a user who hits a delete hold or an owner
// mismatch finds the way out in `swarf docs safety`.
func TestSafetyTopic(t *testing.T) {
	topic := Get("safety")
	if topic == nil {
		t.Fatal("expected safety topic")
	}
	for _, want := range []string{
		"delete hold",
		"hold marker",
		"swarf push --allow-deletes",
		"swarf pull --resync",
		"owner token",
		"swarf push --take-over",
		"machine branch",
		"sync lock",
		"pull_interval",
	} {
		if !strings.Contains(topic.Content, want) {
			t.Errorf("safety topic does not mention %q", want)
		}
	}
	if !strings.Contains(ListTopics(), "safety") {
		t.Fatal("expected 'safety' in the topic list")
	}
}

// TestBackendsTopic pins what 'swarf docs backends' has to say about
// multi-machine git: the machine branch each machine pushes, how a
// remote that every machine pushed as one branch is migrated (the legacy
// branch folded in and retired, or replaced by a tombstone), how swarf
// recognizes a tombstone, and that every machine needs upgrading.
func TestBackendsTopic(t *testing.T) {
	topic := Get("backends")
	if topic == nil {
		t.Fatal("expected backends topic")
	}
	for _, want := range []string{
		"machines/<machine_id>",
		"machine branch",
		"legacy branch",
		"retire",
		"tombstone",
		"SWARF-BRANCHES.md",
		"Swarf-Tombstone: true",
		"swarf push --take-over",
		"swarf pull --resync",
		"pull_interval",
		"Upgrade swarf on every machine",
	} {
		if !strings.Contains(topic.Content, want) {
			t.Errorf("backends topic does not mention %q", want)
		}
	}
}

// TestNoTopicLimitsGitToOneMachine guards against the statements that
// held before the git backend synced between machines: that it is
// single-machine, and that scheduled pull, resync, or --take-over are
// rclone-only.
func TestNoTopicLimitsGitToOneMachine(t *testing.T) {
	for _, topic := range Topics() {
		flat := strings.Join(strings.Fields(topic.Content), " ")
		for _, stale := range []string{
			"single-machine",
			"rclone only",
			"rclone-only",
			"rclone backend only",
			"For more than one machine, use rclone",
			"scheduled pull is not supported",
			"With the git backend, copy the files back",
		} {
			if strings.Contains(flat, stale) {
				t.Errorf("topic %s still says %q", topic.Name, stale)
			}
		}
	}
}
