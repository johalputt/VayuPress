package schema

func init() {
	Global.Register(&Schema{
		Type:     "article.created",
		Version:  "v1",
		Required: []string{"id", "title", "author_id", "published_at"},
		Properties: map[string]PropDef{
			"id":           {Type: "string"},
			"title":        {Type: "string"},
			"author_id":    {Type: "string"},
			"published_at": {Type: "string", Format: "date-time"},
			"slug":         {Type: "string"},
		},
	})

	Global.Register(&Schema{
		Type:     "article.updated",
		Version:  "v1",
		Required: []string{"id", "updated_at"},
		Properties: map[string]PropDef{
			"id":         {Type: "string"},
			"updated_at": {Type: "string", Format: "date-time"},
		},
	})

	Global.Register(&Schema{
		Type:     "article.deleted",
		Version:  "v1",
		Required: []string{"id"},
		Properties: map[string]PropDef{
			"id": {Type: "string"},
		},
	})

	Global.Register(&Schema{
		Type:     "user.registered",
		Version:  "v1",
		Required: []string{"id", "email", "registered_at"},
		Properties: map[string]PropDef{
			"id":            {Type: "string"},
			"email":         {Type: "string"},
			"registered_at": {Type: "string", Format: "date-time"},
		},
	})
}
