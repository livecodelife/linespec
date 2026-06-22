package routes

// Route is a discovered HTTP route endpoint.
type Route struct {
	Method          string
	Path            string
	HandlerRef      string
	MiddlewareChain []string
	Source          SourceLocation
}

// SourceLocation identifies where in source the route is registered (1-indexed).
type SourceLocation struct {
	File   string
	Line   uint32
	Column uint32
}

// Group is a cluster of related routes sharing a grouping identity.
type Group struct {
	Name   string
	Routes []Route
}
