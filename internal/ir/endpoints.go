package ir

import (
	"slices"
	"strings"
)

// Endpoint describes one HTTP endpoint in the standard REST contract that the
// core derives from the entity. The bridge renders the body of each endpoint
// from this map — the HTTP method, route, success status, possible error
// statuses and the authorization plan all come pre-computed, so a bridge only
// translates them into its own framework syntax.
type Endpoint struct {
	Method    string // GET | POST | PUT | PATCH | DELETE
	Path      string // route template relative to the API root (e.g. "api/orders/{id}")
	Operation string // list | get | create | update | patch | delete
	// HasBody is true for endpoints that accept a request body.
	HasBody bool
	// SuccessStatus is the primary success status (200/201/204).
	SuccessStatus int
	// ErrorStatuses are the possible non-success statuses, ascending.
	ErrorStatuses []int
	// Auth is the authorization plan, or nil when the entity declares no
	// permissions (the bridge then applies its "no permissions" default).
	Auth *PermissionPlan
	// Paginated is true for the collection (list) endpoint.
	Paginated bool
	// Concurrency is true when a 409 conflict is possible (optimistic locking).
	Concurrency bool
	// Anonymous is true for project-level endpoints that need no credentials
	// (auth login/register/setup). Entity endpoints always carry an Auth plan.
	Anonymous bool
}

// AuthPolicy returns the authorization policy name for the endpoint's operation
// (Read/Create/Update/Delete), matching the policy names a bridge registers for
// `[Authorize(Policy = ...)]`-style checks.
func (e Endpoint) AuthPolicy() string {
	switch e.Operation {
	case "list", "get":
		return "Read"
	case "create":
		return "Create"
	case "update", "patch":
		return "Update"
	case "delete":
		return "Delete"
	}
	return ""
}

// Endpoints returns the standard REST endpoint contract for the entity, in a
// stable order (list, get, create, update, patch, delete).
func (e Entity) Endpoints() []Endpoint {
	plural := e.NamePlural
	if plural == "" {
		plural = e.Name
	}
	base := "api/" + strings.ToLower(plural)

	common := []int{400, 401, 403, 429}
	getErrors := []int{401, 403, 404, 429}
	updateErrors := []int{400, 401, 403, 404, 429}
	if e.HasOptimisticLock() {
		updateErrors = append(updateErrors, 409)
		slices.Sort(updateErrors)
	}

	// The order of decls is the endpoint contract order; each row is one
	// standard REST operation. ErrorStatuses are cloned per endpoint so a
	// bridge mutating one route cannot corrupt its sibling.
	decls := []struct {
		method, operation, suffix, authOperation string
		hasBody, paginated, concurrent           bool
		success                                  int
		errors                                   []int
	}{
		{method: "GET", operation: "list", authOperation: "read", paginated: true, success: 200, errors: common},
		{method: "GET", operation: "get", suffix: "/{id}", authOperation: "read", success: 200, errors: getErrors},
		{method: "POST", operation: "create", authOperation: "create", hasBody: true, success: 201, errors: common},
		{method: "PUT", operation: "update", suffix: "/{id}", authOperation: "update", hasBody: true, concurrent: true, success: 200, errors: updateErrors},
		{method: "PATCH", operation: "patch", suffix: "/{id}", authOperation: "update", hasBody: true, concurrent: true, success: 200, errors: updateErrors},
		{method: "DELETE", operation: "delete", suffix: "/{id}", authOperation: "delete", success: 204, errors: getErrors},
	}

	eps := make([]Endpoint, 0, len(decls))
	for _, d := range decls {
		eps = append(eps, Endpoint{
			Method:        d.method,
			Path:          base + d.suffix,
			Operation:     d.operation,
			HasBody:       d.hasBody,
			SuccessStatus: d.success,
			ErrorStatuses: slices.Clone(d.errors),
			Auth:          e.PermissionPlan(d.authOperation),
			Paginated:     d.paginated,
			Concurrency:   d.concurrent && e.HasOptimisticLock(),
		})
	}
	return eps
}

// AuthEndpoints returns the project-level auth endpoint contract (login /
// register / me / setup) when authentication is enabled. It shares the same
// Endpoint shape as entity routes; auth endpoints carry no permission plan and
// mark their anonymous routes with Anonymous.
func (p *Project) AuthEndpoints() []Endpoint {
	if !p.HasAuth() {
		return nil
	}
	entity := p.Auth.Entity
	if entity == "" {
		return nil
	}
	base := "api/" + entity

	// Same shape as entity routes, but no permission plan: anonymous routes are
	// marked with Anonymous and /me stays authenticated.
	decls := []struct {
		enabled                  bool
		method, operation, route string
		hasBody, anonymous       bool
		success                  int
		errors                   []int
	}{
		{p.Auth.Endpoints.HasLogin, "POST", "login", "/login", true, true, 200, []int{400, 401, 429}},
		{p.Auth.Endpoints.HasRegister, "POST", "register", "/register", true, true, 201, []int{400, 409, 429}},
		{p.Auth.Endpoints.HasMe, "GET", "me", "/me", false, false, 200, []int{401, 404, 429}},
		{p.Auth.Endpoints.HasSetup, "POST", "setup", "/setup", true, true, 201, []int{400, 409, 429}},
	}

	var endpoints []Endpoint
	for _, d := range decls {
		if !d.enabled {
			continue
		}
		endpoints = append(endpoints, Endpoint{
			Method:        d.method,
			Path:          base + d.route,
			Operation:     d.operation,
			HasBody:       d.hasBody,
			SuccessStatus: d.success,
			ErrorStatuses: slices.Clone(d.errors),
			Anonymous:     d.anonymous,
		})
	}
	return endpoints
}
