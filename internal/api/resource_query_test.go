package api

import (
	"csgclaw/internal/agentengine"
	"csgclaw/internal/agentengine/enginetest"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestResourceQueryValidation(t *testing.T) {
	for _, query := range []string{"pagination=other", "pagination=page&per=101", "pagination=page&page=-1", "pagination=page&limit=20", "pagination=cursor&page=2", "pagination=cursor&enabled=maybe", "pagination=page&cursor="} {
		if _, err := parseResourceQuery(httptest.NewRequest("GET", "/?"+query, nil)); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
	if q, err := parseResourceQuery(httptest.NewRequest("GET", "/", nil)); err != nil || q != nil {
		t.Fatalf("legacy query: %+v %v", q, err)
	}
}

func TestAgentResourcePagesHTTP(t *testing.T) {
	engine := enginetest.NewMemoryClient(agentengine.Agent{ID: "agent", Spec: agentengine.AgentSpec{Skills: []string{"zulu", "alpha", "beta"}, MCPServers: map[string]agentengine.MCPServerConfig{"zulu": {"url": "https://example.com/z"}, "alpha": {"url": "https://example.com/a", "enabled": false}}}, Status: agentengine.AgentStatus{SkillSummaries: []agentengine.SkillSummary{{Name: "alpha", Description: "A", Enabled: true}, {Name: "beta", Description: "B", Enabled: true}, {Name: "zulu", Description: "Z", Enabled: true}}}})
	h := NewHandler(AgentServices{}, engine, nil, nil, nil, nil, nil)
	for _, resource := range []string{"skill-summaries", "mcp-servers"} {
		for page, want := range []string{"alpha", map[string]string{"skill-summaries": "beta", "mcp-servers": "zulu"}[resource]} {
			w := httptest.NewRecorder()
			path := "/api/v1/agents/agent/" + resource + "?pagination=page&per=1&page=" + string(rune('1'+page))
			h.Routes().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 200 || w.Header().Get("ETag") == "" {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
			var result resourcePage[struct {
				Name string `json:"name"`
			}]
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Items) != 1 || result.Items[0].Name != want {
				t.Fatalf("%s: %s %v", path, w.Body, err)
			}
		}
	}
}
