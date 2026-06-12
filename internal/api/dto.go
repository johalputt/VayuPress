package api

// CreateArticleRequest is the JSON body for POST /api/v1/articles.
type CreateArticleRequest struct {
	Title   string   `json:"title"`
	Slug    string   `json:"slug"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// UpdateArticleRequest is the JSON body for PUT /api/v1/articles/{slug}.
// Nil pointer fields are left unchanged.
type UpdateArticleRequest struct {
	Title   *string  `json:"title"`
	Content *string  `json:"content"`
	Tags    []string `json:"tags"`
}
