package ai

import (
	"errors"
	"fmt"
)

// AgentStep defines a single processing step in an AI agent pipeline.
type AgentStep struct {
	Name   string                 `json:"name"`
	Action string                 `json:"action"` // "embed", "classify", "summarize"
	Params map[string]interface{} `json:"params,omitempty"`
}

// AgentDefinition describes a user-defined AI agent.
type AgentDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Steps       []AgentStep `json:"steps"`
}

// AgentResult holds the output of an agent execution.
type AgentResult struct {
	AgentName string                 `json:"agent_name"`
	Outputs   map[string]interface{} `json:"outputs"`
}

// AgentRunner executes AgentDefinitions.
type AgentRunner struct {
	embedder Embedder
}

// NewAgentRunner creates an AgentRunner with the given embedder.
func NewAgentRunner(e Embedder) *AgentRunner {
	return &AgentRunner{embedder: e}
}

// Run executes an agent with the given input context.
func (r *AgentRunner) Run(def AgentDefinition, input map[string]interface{}) (*AgentResult, error) {
	if len(def.Steps) == 0 {
		return nil, errors.New("ai: agent has no steps")
	}
	outputs := make(map[string]interface{})
	ctx := make(map[string]interface{}, len(input))
	for k, v := range input {
		ctx[k] = v
	}

	for _, step := range def.Steps {
		out, err := r.execStep(step, ctx)
		if err != nil {
			return nil, fmt.Errorf("ai: step %q: %w", step.Name, err)
		}
		outputs[step.Name] = out
		ctx[step.Name+"_result"] = out
	}
	return &AgentResult{AgentName: def.Name, Outputs: outputs}, nil
}

func (r *AgentRunner) execStep(step AgentStep, ctx map[string]interface{}) (interface{}, error) {
	switch step.Action {
	case "embed":
		text, _ := ctx["text"].(string)
		if text == "" {
			return nil, errors.New("embed: no text in context")
		}
		return r.embedder.Embed(text)
	case "summarize":
		text, _ := ctx["text"].(string)
		if len(text) > 200 {
			return text[:200] + "...", nil
		}
		return text, nil
	case "classify":
		text, _ := ctx["text"].(string)
		labels, _ := step.Params["labels"].([]interface{})
		if len(labels) == 0 {
			return "unknown", nil
		}
		// Nearest-label by cosine similarity of embeddings.
		queryEmb, err := r.embedder.Embed(text)
		if err != nil {
			return nil, err
		}
		best, bestSim := fmt.Sprintf("%v", labels[0]), float32(-2)
		for _, label := range labels {
			lstr := fmt.Sprintf("%v", label)
			le, err := r.embedder.Embed(lstr)
			if err != nil {
				continue
			}
			if s := CosineSimilarity(queryEmb, le); s > bestSim {
				bestSim, best = s, lstr
			}
		}
		return best, nil
	default:
		return nil, fmt.Errorf("unknown action: %s", step.Action)
	}
}
