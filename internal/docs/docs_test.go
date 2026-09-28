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
