package routes

import "strings"

// expandResources expands a `resources :name` declaration to its 7 conventional REST routes.
// prefix is the accumulated path prefix from surrounding namespace/scope blocks.
func expandResources(resourceName, prefix string) []Route {
	base := joinPaths(prefix, "/"+resourceName)
	ctrl := toControllerName(resourceName)
	return []Route{
		{Method: "GET", Path: base, HandlerRef: ctrl + "#index"},
		{Method: "POST", Path: base, HandlerRef: ctrl + "#create"},
		{Method: "GET", Path: base + "/new", HandlerRef: ctrl + "#new"},
		{Method: "GET", Path: base + "/:id/edit", HandlerRef: ctrl + "#edit"},
		{Method: "GET", Path: base + "/:id", HandlerRef: ctrl + "#show"},
		{Method: "PUT", Path: base + "/:id", HandlerRef: ctrl + "#update"},
		{Method: "PATCH", Path: base + "/:id", HandlerRef: ctrl + "#update"},
		{Method: "DELETE", Path: base + "/:id", HandlerRef: ctrl + "#destroy"},
	}
}

// expandResource expands a singular `resource :name` declaration to 6 conventional REST routes.
func expandResource(resourceName, prefix string) []Route {
	base := joinPaths(prefix, "/"+resourceName)
	ctrl := toControllerName(resourceName)
	return []Route{
		{Method: "POST", Path: base, HandlerRef: ctrl + "#create"},
		{Method: "GET", Path: base, HandlerRef: ctrl + "#show"},
		{Method: "GET", Path: base + "/new", HandlerRef: ctrl + "#new"},
		{Method: "GET", Path: base + "/edit", HandlerRef: ctrl + "#edit"},
		{Method: "PUT", Path: base, HandlerRef: ctrl + "#update"},
		{Method: "PATCH", Path: base, HandlerRef: ctrl + "#update"},
		{Method: "DELETE", Path: base, HandlerRef: ctrl + "#destroy"},
	}
}

// toControllerName converts a snake_case resource name to a Rails controller class name.
// "users" → "UsersController", "user_profiles" → "UserProfilesController"
func toControllerName(name string) string {
	parts := strings.Split(name, "_")
	var b strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			b.WriteString(strings.ToUpper(p[:1]))
			b.WriteString(p[1:])
		}
	}
	b.WriteString("Controller")
	return b.String()
}

// controllerFromHandlerRef extracts the controller class name from a handler ref string.
// "UsersController#index" → "UsersController", anything else → file.
func controllerFromHandlerRef(handlerRef, file string) string {
	if idx := strings.Index(handlerRef, "#"); idx > 0 {
		return handlerRef[:idx]
	}
	return file
}
