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

// HasStatus reports whether status is among the endpoint's possible statuses.
func (e Endpoint) HasStatus(status int) bool {
	return e.SuccessStatus == status || slices.Contains(e.ErrorStatuses, status)
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

	eps := []Endpoint{
		{
			Method: "GET", Path: base, Operation: "list",
			HasBody: false, SuccessStatus: 200,
			ErrorStatuses: slices.Clone(common), Auth: e.PermissionPlan("read"),
			Paginated: true, Concurrency: false,
		},
		{
			Method: "GET", Path: base + "/{id}", Operation: "get",
			HasBody: false, SuccessStatus: 200,
			ErrorStatuses: slices.Clone(getErrors), Auth: e.PermissionPlan("read"),
		},
		{
			Method: "POST", Path: base, Operation: "create",
			HasBody: true, SuccessStatus: 201,
			ErrorStatuses: slices.Clone(common), Auth: e.PermissionPlan("create"),
		},
		{
			Method: "PUT", Path: base + "/{id}", Operation: "update",
			HasBody: true, SuccessStatus: 200,
			ErrorStatuses: slices.Clone(updateErrors), Auth: e.PermissionPlan("update"),
			Concurrency: e.HasOptimisticLock(),
		},
		{
			Method: "PATCH", Path: base + "/{id}", Operation: "patch",
			HasBody: true, SuccessStatus: 200,
			ErrorStatuses: slices.Clone(updateErrors), Auth: e.PermissionPlan("update"),
			Concurrency: e.HasOptimisticLock(),
		},
		{
			Method: "DELETE", Path: base + "/{id}", Operation: "delete",
			HasBody: false, SuccessStatus: 204,
			ErrorStatuses: slices.Clone(getErrors), Auth: e.PermissionPlan("delete"),
		},
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

	var decls []Endpoint
	if p.Auth.Endpoints.HasLogin {
		decls = append(decls, Endpoint{
			Method: "POST", Path: base + "/login", Operation: "login",
			HasBody: true, SuccessStatus: 200,
			ErrorStatuses: []int{400, 401, 429}, Anonymous: true,
		})
	}
	if p.Auth.Endpoints.HasRegister {
		decls = append(decls, Endpoint{
			Method: "POST", Path: base + "/register", Operation: "register",
			HasBody: true, SuccessStatus: 201,
			ErrorStatuses: []int{400, 409, 429}, Anonymous: true,
		})
	}
	if p.Auth.Endpoints.HasMe {
		decls = append(decls, Endpoint{
			Method: "GET", Path: base + "/me", Operation: "me",
			HasBody: false, SuccessStatus: 200,
			ErrorStatuses: []int{401, 404, 429},
		})
	}
	if p.Auth.Endpoints.HasSetup {
		decls = append(decls, Endpoint{
			Method: "POST", Path: base + "/setup", Operation: "setup",
			HasBody: true, SuccessStatus: 201,
			ErrorStatuses: []int{400, 409, 429}, Anonymous: true,
		})
	}
	return decls
}
