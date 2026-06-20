// Package graphqlapi exposes a read-only GraphQL endpoint over the public
// content model. It is deliberately query-only — there are no mutations — so
// the GraphQL surface can never bypass the governed, API-key-protected write
// path. Mutations remain the exclusive province of the REST admin API.
//
// The schema is intentionally small and stable:
//
//	type Article  { id, title, slug, content, excerpt, tags, readingMinutes,
//	                 wordCount, createdAt, updatedAt }
//	type Query    { article(slug): Article
//	                articles(tag, limit, offset): [Article!]!
//	                tags: [TagCount!]!
//	                searchArticles(query, limit): [Article!]! }
package graphqlapi

import (
	"context"
	"regexp"
	"strings"

	"github.com/graphql-go/graphql"

	"github.com/johalputt/vayupress/internal/db"
)

// Resolver is the data-access seam the schema depends on. It is satisfied by a
// thin adapter in package main so this package stays free of HTTP/app wiring.
type Resolver interface {
	// Article returns one article by slug, or (nil, nil) when not found.
	Article(ctx context.Context, slug string) (*db.Article, error)
	// Articles returns a page of articles, optionally filtered by tag.
	Articles(ctx context.Context, tag string, limit, offset int) ([]db.Article, error)
	// Tags returns tag names with their article counts.
	Tags(ctx context.Context) (map[string]int, error)
	// Search returns articles matching a free-text query.
	Search(ctx context.Context, query string, limit int) ([]db.Article, error)
}

// Service holds the compiled schema and resolver.
type Service struct {
	schema   graphql.Schema
	resolver Resolver
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

// stripHTML returns plain text for excerpts/word counts.
func stripHTML(s string) string { return strings.TrimSpace(htmlTagRe.ReplaceAllString(s, "")) }

// wordCount counts whitespace-delimited words in the article body.
func wordCount(content string) int { return len(strings.Fields(stripHTML(content))) }

// readingMinutes estimates reading time at 200 words/minute (min 1).
func readingMinutes(content string) int {
	w := wordCount(content)
	if w < 200 {
		return 1
	}
	return (w + 199) / 200
}

// excerpt returns up to n characters of plain text with an ellipsis.
func excerpt(content string, n int) string {
	t := stripHTML(content)
	if len(t) > n {
		return t[:n] + "…"
	}
	return t
}

// New compiles the schema against the given resolver.
func New(r Resolver) (*Service, error) {
	s := &Service{resolver: r}

	articleType := graphql.NewObject(graphql.ObjectConfig{
		Name: "Article",
		Fields: graphql.Fields{
			"id":    &graphql.Field{Type: graphql.String},
			"title": &graphql.Field{Type: graphql.String},
			"slug":  &graphql.Field{Type: graphql.String},
			"content": &graphql.Field{
				Type: graphql.String,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if a, ok := p.Source.(db.Article); ok {
						return a.Content, nil
					}
					return nil, nil
				},
			},
			"excerpt": &graphql.Field{
				Type: graphql.String,
				Args: graphql.FieldConfigArgument{
					"length": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 200},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					a, ok := p.Source.(db.Article)
					if !ok {
						return nil, nil
					}
					n, _ := p.Args["length"].(int)
					if n <= 0 {
						n = 200
					}
					return excerpt(a.Content, n), nil
				},
			},
			"tags": &graphql.Field{Type: graphql.NewList(graphql.String)},
			"wordCount": &graphql.Field{
				Type: graphql.Int,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if a, ok := p.Source.(db.Article); ok {
						return wordCount(a.Content), nil
					}
					return 0, nil
				},
			},
			"readingMinutes": &graphql.Field{
				Type: graphql.Int,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if a, ok := p.Source.(db.Article); ok {
						return readingMinutes(a.Content), nil
					}
					return 0, nil
				},
			},
			"createdAt": &graphql.Field{
				Type: graphql.DateTime,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if a, ok := p.Source.(db.Article); ok {
						return a.CreatedAt, nil
					}
					return nil, nil
				},
			},
			"updatedAt": &graphql.Field{
				Type: graphql.DateTime,
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					if a, ok := p.Source.(db.Article); ok {
						return a.UpdatedAt, nil
					}
					return nil, nil
				},
			},
		},
	})

	tagCountType := graphql.NewObject(graphql.ObjectConfig{
		Name: "TagCount",
		Fields: graphql.Fields{
			"name":  &graphql.Field{Type: graphql.String},
			"count": &graphql.Field{Type: graphql.Int},
		},
	})

	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			"article": &graphql.Field{
				Type: articleType,
				Args: graphql.FieldConfigArgument{
					"slug": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					slug, _ := p.Args["slug"].(string)
					art, err := s.resolver.Article(p.Context, slug)
					if err != nil || art == nil {
						return nil, err
					}
					return *art, nil
				},
			},
			"articles": &graphql.Field{
				Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(articleType))),
				Args: graphql.FieldConfigArgument{
					"tag":    &graphql.ArgumentConfig{Type: graphql.String, DefaultValue: ""},
					"limit":  &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 20},
					"offset": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 0},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					tag, _ := p.Args["tag"].(string)
					limit, _ := p.Args["limit"].(int)
					offset, _ := p.Args["offset"].(int)
					if limit <= 0 || limit > 100 {
						limit = 20
					}
					if offset < 0 {
						offset = 0
					}
					arts, err := s.resolver.Articles(p.Context, tag, limit, offset)
					if err != nil {
						return nil, err
					}
					return toInterfaceSlice(arts), nil
				},
			},
			"tags": &graphql.Field{
				Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(tagCountType))),
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					counts, err := s.resolver.Tags(p.Context)
					if err != nil {
						return nil, err
					}
					out := make([]map[string]interface{}, 0, len(counts))
					for name, c := range counts {
						out = append(out, map[string]interface{}{"name": name, "count": c})
					}
					return out, nil
				},
			},
			"searchArticles": &graphql.Field{
				Type: graphql.NewNonNull(graphql.NewList(graphql.NewNonNull(articleType))),
				Args: graphql.FieldConfigArgument{
					"query": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
					"limit": &graphql.ArgumentConfig{Type: graphql.Int, DefaultValue: 20},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					query, _ := p.Args["query"].(string)
					limit, _ := p.Args["limit"].(int)
					if limit <= 0 || limit > 100 {
						limit = 20
					}
					arts, err := s.resolver.Search(p.Context, query, limit)
					if err != nil {
						return nil, err
					}
					return toInterfaceSlice(arts), nil
				},
			},
		},
	})

	schema, err := graphql.NewSchema(graphql.SchemaConfig{Query: rootQuery})
	if err != nil {
		return nil, err
	}
	s.schema = schema
	return s, nil
}

// toInterfaceSlice converts []db.Article into []interface{} of db.Article values
// (the article type's resolvers expect a db.Article source).
func toInterfaceSlice(arts []db.Article) []interface{} {
	out := make([]interface{}, len(arts))
	for i, a := range arts {
		out[i] = a
	}
	return out
}

// Execute runs a GraphQL query and returns the result. variables and
// operationName may be nil/empty.
func (s *Service) Execute(ctx context.Context, query string, variables map[string]interface{}, operationName string) *graphql.Result {
	return graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  query,
		VariableValues: variables,
		OperationName:  operationName,
		Context:        ctx,
	})
}
