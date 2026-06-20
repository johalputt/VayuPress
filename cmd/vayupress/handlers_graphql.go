package main

// handlers_graphql.go — read-only public GraphQL endpoint (Tier 4).
//
// GraphQL is exposed at POST /api/v1/graphql (and GET for simple queries). It is
// query-only: there are no mutations, so it can never bypass the governed,
// API-key-protected REST write path. The schema lives in internal/graphqlapi;
// this file provides the HTTP transport and a resolver adapter backed by the
// existing article repository and search service.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/graphqlapi"
)

// gqlResolver adapts App's data services to the graphqlapi.Resolver interface.
type gqlResolver struct{ a *App }

func (g gqlResolver) Article(ctx context.Context, slug string) (*dbpkg.Article, error) {
	art, err := g.a.articles.Repo.Get(ctx, slug)
	if err != nil {
		// Not-found is a nil result, not an error, for GraphQL.
		return nil, nil //nolint:nilerr
	}
	return &art, nil
}

func (g gqlResolver) Articles(ctx context.Context, tag string, limit, offset int) ([]dbpkg.Article, error) {
	// The repo paginates by (page, limit); to honour an arbitrary offset exactly
	// (not just page-aligned offsets) we fetch a window covering offset+limit
	// rows from page 1 and slice. The window is bounded so a huge offset cannot
	// force an unbounded scan.
	if offset < 0 {
		offset = 0
	}
	window := offset + limit
	const maxWindow = 1000
	if window > maxWindow {
		window = maxWindow
	}
	arts, _, err := g.a.articles.Repo.List(ctx, 1, window, tag)
	if err != nil {
		return nil, err
	}
	if offset >= len(arts) {
		return nil, nil
	}
	end := offset + limit
	if end > len(arts) {
		end = len(arts)
	}
	return arts[offset:end], nil
}

func (g gqlResolver) Tags(ctx context.Context) (map[string]int, error) {
	csvs, err := g.a.articles.Repo.AllTagCSVs(ctx)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, csv := range csvs {
		for _, t := range strings.Split(csv, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				counts[t]++
			}
		}
	}
	return counts, nil
}

func (g gqlResolver) Search(ctx context.Context, query string, limit int) ([]dbpkg.Article, error) {
	res, err := g.a.search.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dbpkg.Article, 0, len(res.Hits))
	for _, h := range res.Hits {
		if art, err := g.a.articles.Repo.Get(ctx, h.Slug); err == nil {
			out = append(out, art)
		}
	}
	return out, nil
}

// handleGraphQL serves the GraphQL endpoint. POST accepts a JSON body
// {query, variables, operationName}; GET accepts ?query=&variables=.
func (a *App) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	if a.graphql == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "graphql-disabled", "GraphQL not initialised", "")
		return
	}
	var (
		query     string
		variables map[string]interface{}
		opName    string
	)
	switch r.Method {
	case http.MethodGet:
		query = r.URL.Query().Get("query")
		opName = r.URL.Query().Get("operationName")
		if v := r.URL.Query().Get("variables"); v != "" {
			_ = json.Unmarshal([]byte(v), &variables)
		}
	default:
		var body struct {
			Query         string                 `json:"query"`
			Variables     map[string]interface{} `json:"variables"`
			OperationName string                 `json:"operationName"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
			return
		}
		query, variables, opName = body.Query, body.Variables, body.OperationName
	}
	if strings.TrimSpace(query) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "empty-query", "No GraphQL query provided", "")
		return
	}

	result := a.graphql.Execute(r.Context(), query, variables, opName)
	// GraphQL convention: 200 even when the result carries field errors.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(result)
}

// initGraphQL builds the GraphQL service. Called from main after the article
// service and search are wired. Failure is non-fatal (endpoint returns 503).
func (a *App) initGraphQL() {
	svc, err := graphqlapi.New(gqlResolver{a: a})
	if err != nil {
		return
	}
	a.graphql = svc
}
