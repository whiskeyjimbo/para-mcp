package domain

// QueryOption configures a QueryRequest.
type QueryOption func(*QueryRequest)

// NewQueryRequest builds a QueryRequest from zero or more options.
func NewQueryRequest(opts ...QueryOption) QueryRequest {
	var q QueryRequest
	for _, o := range opts {
		o(&q)
	}
	return q
}

// WithQueryFilter sets the content filter.
func WithQueryFilter(f Filter) QueryOption { return func(q *QueryRequest) { q.Filter = f } }

// WithQueryAllowedScopes sets the authorization scope list.
func WithQueryAllowedScopes(scopes []ScopeID) QueryOption {
	return func(q *QueryRequest) { q.AllowedScopes = scopes }
}

// WithQuerySort sets the sort field and direction.
func WithQuerySort(field SortField, desc bool) QueryOption {
	return func(q *QueryRequest) { q.Sort = field; q.Desc = desc }
}

// WithQueryPagination sets limit and offset.
func WithQueryPagination(limit, offset int) QueryOption {
	return func(q *QueryRequest) { q.Limit = limit; q.Offset = offset }
}

// WithQueryCursor sets the cursor for cursor-based pagination.
func WithQueryCursor(cursor string) QueryOption {
	return func(q *QueryRequest) { q.Cursor = cursor }
}
