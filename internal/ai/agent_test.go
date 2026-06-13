package ai_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/ai"
)

func TestEmbedAndSimilarity(t *testing.T) {
	e := ai.NewLocalEmbedder(64)
	v1, err := e.Embed("hello world")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := e.Embed("hello world")
	if err != nil {
		t.Fatal(err)
	}
	if len(v1) != 64 {
		t.Errorf("expected 64 dims, got %d", len(v1))
	}
	// Same text → similarity ≈ 1.0
	sim := ai.CosineSimilarity(v1, v2)
	if sim < 0.999 {
		t.Errorf("expected similarity ~1, got %f", sim)
	}
}

func TestAgentSummarize(t *testing.T) {
	runner := ai.NewAgentRunner(ai.NewLocalEmbedder(64))
	def := ai.AgentDefinition{
		Name: "summarizer",
		Steps: []ai.AgentStep{
			{Name: "summary", Action: "summarize"},
		},
	}
	long := "This is a very long text that exceeds two hundred characters and should be truncated by the summarize step to produce a shorter output for display purposes in the VayuPress UI"
	res, err := runner.Run(def, map[string]interface{}{"text": long})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	summary := res.Outputs["summary"].(string)
	if len(summary) > 204 {
		t.Errorf("summary too long: %d chars", len(summary))
	}
}

func TestAgentClassify(t *testing.T) {
	runner := ai.NewAgentRunner(ai.NewLocalEmbedder(64))
	def := ai.AgentDefinition{
		Name: "classifier",
		Steps: []ai.AgentStep{
			{Name: "label", Action: "classify", Params: map[string]interface{}{
				"labels": []interface{}{"security", "performance", "governance"},
			}},
		},
	}
	res, err := runner.Run(def, map[string]interface{}{"text": "encryption key management"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	label := res.Outputs["label"].(string)
	if label == "" {
		t.Error("expected a label")
	}
}
