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
