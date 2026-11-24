package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/interactive"

	"github.com/spf13/cobra"
)

// scenario is one starter model. Templates are kept in code: init is the
// zero-entry point, so it must work from a bare directory with no bridge
// checkout or network access.
type scenario struct {
	id          string
	label       string
	description string
	template    string
}

var scenarios = []scenario{
	{
		id:          "shop",
		label:       "shop",
		description: "products, categories, orders and order items",
		template:    shopScenario,
	},
	{
		id:          "saas",
		label:       "saas",
		description: "users, workspaces, memberships, projects and subscriptions",
		template:    saaSScenario,
	},
	{
		id:          "crm",
		label:       "crm",
		description: "companies, contacts, deals and activities",
		template:    crmScenario,
	},
	{
		id:          "blog",
		label:       "blog",
		description: "posts, comments, tags and many-to-many relations",
		template:    blogScenario,
	},
}

func scenarioTemplate(id string) (string, bool) {
	for _, s := range scenarios {
		if s.id == id {
			return s.template, true
		}
	}
	return "", false
}

// renderScenario substitutes the project name as a single-quoted YAML scalar.
func renderScenario(id, name string) (string, error) {
	tmpl, ok := scenarioTemplate(id)
	if !ok {
		return "", fmt.Errorf("unknown scenario %q: use shop, saas, crm or blog", id)
	}
	escaped := strings.ReplaceAll(name, "'", "''")
	return strings.ReplaceAll(tmpl, "{{PROJECT_NAME}}", escaped), nil
}

// defaultProjectName derives a usable name from the working directory so an
// interactive `init` needs no extra text prompt.
func defaultProjectName() string {
	wd, err := os.Getwd()
	if err != nil || filepath.Base(wd) == "" || filepath.Base(wd) == string(filepath.Separator) {
		return "DomainCraft Project"
	}
	return filepath.Base(wd)
}

func newInitCmd() *cobra.Command {
	var scenarioFlag string
	var nameFlag string
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Bootstrap a starter domain.yaml and generate code",
		Long: "Choose a scenario (shop, saas, crm, blog), write a starter domain.yaml and generate code.\n" +
			"Non-interactive runs require --scenario. Add --bridge to generate immediately; without it the model is written and the generate command is printed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := commandLogger(cmd)

			selected := scenarioFlag
			if selected == "" {
				if nonInteractive || !interactive.IsTerminal() {
					return fmt.Errorf("--scenario is required in non-interactive mode (shop|saas|crm|blog)")
				}
				options := make([]interactive.Option, 0, len(scenarios))
				for _, s := range scenarios {
					options = append(options, interactive.Option{Label: s.label, Description: s.description})
				}
				idx, err := interactive.SelectOption("Select a starter scenario", "Writes "+domainFile+" and offers to generate", options)
				if err != nil {
					return err
				}
				selected = scenarios[idx].id
			}

			name := nameFlag
			if name == "" {
				name = defaultProjectName()
			}
			content, err := renderScenario(selected, name)
			if err != nil {
				return err
			}

			if _, err := os.Stat(domainFile); err == nil && !force {
				return fmt.Errorf("%s already exists; use --force to overwrite", domainFile)
			} else if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("check %s: %w", domainFile, err)
			}
			if err := fsutil.AtomicWrite(domainFile, []byte(content)); err != nil {
				return fmt.Errorf("write %s: %w", domainFile, err)
			}
			log.Success("Created %s from the %s scenario", domainFile, selected)

			// Generate when explicitly requested, or after an interactive
			// confirmation. Non-interactive runs without --bridge stop here —
			// they cannot pick a bridge on the user's behalf.
			generate := bridgePath != ""
			if !generate && interactive.IsTerminal() && !nonInteractive {
				generate, err = interactive.PromptYesNo("Generate code now?", "Runs domaincraft generate with the selected bridge")
				if err != nil {
					return err
				}
			}
			if !generate {
				fmt.Fprintf(cmd.OutOrStdout(), "Next: domaincraft validate --domain %s\nthen: domaincraft generate --domain %s --bridge <id>\n", domainFile, domainFile)
				return nil
			}

			genCmd := newGenerateCmd()
			genCmd.SetOut(cmd.OutOrStdout())
			genCmd.SetErr(cmd.ErrOrStderr())
			genCmd.SetIn(cmd.InOrStdin())
			return runGenerate(genCmd, nil)
		},
	}

	cmd.Flags().StringVar(&scenarioFlag, "scenario", "", "starter scenario: shop, saas, crm or blog (interactive when omitted)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "project name (default: current directory name)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing domain.yaml")
	return cmd
}

const shopScenario = `project:
  name: '{{PROJECT_NAME}}'
  description: Starter e-commerce domain model
  version: 1.0.0

database: postgresql
api_style: rest

auth:
  type: jwt
  entity: User
  roles: [Admin, Customer]
  endpoints:
    login: true
    register: true
    me: true
    setup: true

enums:
  Role: [Admin, Customer]
  OrderStatus: [PENDING, PAID, SHIPPED, DELIVERED, CANCELLED]
  ProductStatus: [DRAFT, PUBLISHED, ARCHIVED]

entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
      name: string [required, max:100]
      role: enum(Role) [required, default:Customer]
    permissions:
      read: [Admin, "@Owner"]
      create: ["*"]
      update: ["@Owner", Admin]
      delete: [Admin]

  Category:
    fields:
      id: uuid [primary]
      name: string [required, unique]
      slug: string [required, unique]
    permissions:
      read: ["*"]
      create: [Admin]
      update: [Admin]
      delete: [Admin]

  Product:
    features: [audit, optimistic_lock]
    fields:
      id: uuid [primary]
      title: string [required, min:2, max:200]
      description: text [optional]
      price: decimal [required, gte:0]
      stock: int [required, default:0]
      status: enum(ProductStatus) [default:DRAFT]
      categoryId: relation(Category) [optional, on_delete:set_null]
    permissions:
      read: ["*"]
      create: [Admin]
      update: [Admin]
      delete: [Admin]

  Order:
    features: [audit]
    fields:
      id: uuid [primary]
      number: string [required, unique]
      total: decimal [required, gte:0]
      status: enum(OrderStatus) [default:PENDING]
      customerId: relation(User) [required, on_delete:restrict]
    permissions:
      read: [Admin, "@Owner"]
      create: [Customer, Admin]
      update: [Admin]
      delete: [Admin]

  OrderItem:
    fields:
      id: uuid [primary]
      quantity: int [required, gt:0]
      unitPrice: decimal [required, gte:0]
      orderId: relation(Order) [required, on_delete:cascade]
      productId: relation(Product) [required, on_delete:restrict]
    permissions:
      read: [Admin, "@Owner"]
      create: [Customer, Admin]
      update: [Admin]
      delete: [Admin]
`

const saaSScenario = `project:
  name: '{{PROJECT_NAME}}'
  description: Starter multi-tenant SaaS domain model
  version: 1.0.0

database: postgresql
api_style: rest

auth:
  type: jwt
  entity: User
  roles: [Admin, Owner, Member]
  endpoints:
    login: true
    register: true
    me: true
    setup: true

enums:
  Role: [Admin, Owner, Member]
  WorkspaceRole: [Owner, Admin, Member]
  ProjectStatus: [ACTIVE, ARCHIVED]
  Plan: [FREE, PRO, TEAM]

entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
      name: string [required, max:100]
      role: enum(Role) [required, default:Member]
    permissions:
      read: [Member, Admin]
      create: ["*"]
      update: ["@Owner", Admin]
      delete: [Admin]

  Workspace:
    features: [audit]
    fields:
      id: uuid [primary]
      name: string [required, min:2, max:100]
      slug: string [required, unique]
      ownerId: relation(User) [required, on_delete:restrict]
    permissions:
      read: [Member, Admin]
      create: ["*"]
      update: ["@Owner", Admin]
      delete: [Admin]

  Membership:
    fields:
      id: uuid [primary]
      role: enum(WorkspaceRole) [default:Member]
      workspaceId: relation(Workspace) [required, on_delete:cascade]
      userId: relation(User) [required, on_delete:cascade]
    permissions:
      read: [Member, Admin]
      create: [Owner, Admin]
      update: [Owner, Admin]
      delete: [Owner, Admin]

  Project:
    features: [audit, soft_delete]
    fields:
      id: uuid [primary]
      name: string [required, min:2, max:100]
      status: enum(ProjectStatus) [default:ACTIVE]
      workspaceId: relation(Workspace) [required, on_delete:cascade]
    permissions:
      read: [Member, Admin]
      create: [Member, Admin]
      update: [Member, Admin]
      delete: [Owner, Admin]

  Subscription:
    features: [audit]
    fields:
      id: uuid [primary]
      plan: enum(Plan) [default:FREE]
      seats: int [required, default:1, gte:1]
      workspaceId: relation(Workspace) [required, on_delete:cascade]
    permissions:
      read: [Owner, Admin]
      create: [Admin]
      update: [Admin]
      delete: [Admin]
`

const crmScenario = `project:
  name: '{{PROJECT_NAME}}'
  description: Starter CRM domain model
  version: 1.0.0

database: postgresql
api_style: rest

auth:
  type: jwt
  entity: User
  roles: [Admin, Manager, Viewer]
  endpoints:
    login: true
    register: true
    me: true
    setup: true

enums:
  Role: [Admin, Manager, Viewer]
  Industry: [SOFTWARE, FINANCE, RETAIL, HEALTHCARE, OTHER]
  DealStage: [NEW, QUALIFIED, PROPOSAL, WON, LOST]
  ActivityType: [CALL, EMAIL, MEETING, NOTE]

entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
      name: string [required, max:100]
      role: enum(Role) [required, default:Viewer]
    permissions:
      read: [Manager, Admin]
      create: ["*"]
      update: ["@Owner", Admin]
      delete: [Admin]

  Company:
    features: [audit]
    fields:
      id: uuid [primary]
      name: string [required, unique, min:2, max:200]
      industry: enum(Industry) [default:OTHER]
      website: string [optional, url]
      ownerId: relation(User) [required, on_delete:restrict]
    permissions:
      read: [Viewer, Manager, Admin]
      create: [Manager, Admin]
      update: [Manager, Admin]
      delete: [Admin]

  Contact:
    features: [audit, soft_delete]
    fields:
      id: uuid [primary]
      firstName: string [required, min:2, max:50]
      lastName: string [required, min:2, max:50]
      email: string [required, email]
      phone: string [optional]
      companyId: relation(Company) [optional, on_delete:set_null]
    permissions:
      read: [Viewer, Manager, Admin]
      create: [Manager, Admin]
      update: [Manager, Admin]
      delete: [Admin]

  Deal:
    features: [audit, optimistic_lock]
    fields:
      id: uuid [primary]
      title: string [required, min:2, max:200]
      value: decimal [required, gte:0]
      stage: enum(DealStage) [default:NEW]
      companyId: relation(Company) [required, on_delete:cascade]
      ownerId: relation(User) [required, on_delete:restrict]
    permissions:
      read: [Viewer, Manager, Admin]
      create: [Manager, Admin]
      update: ["@Owner", Manager, Admin]
      delete: [Admin]

  Activity:
    fields:
      id: uuid [primary]
      type: enum(ActivityType) [required]
      notes: text [optional]
      occurredAt: datetime [required]
      contactId: relation(Contact) [required, on_delete:cascade]
    permissions:
      read: [Viewer, Manager, Admin]
      create: [Manager, Admin]
      update: [Manager, Admin]
      delete: [Admin]
`

const blogScenario = `project:
  name: '{{PROJECT_NAME}}'
  description: Starter publishing domain model
  version: 1.0.0

database: postgresql
api_style: rest

auth:
  type: jwt
  entity: User
  roles: [Admin, Author, Reader]
  endpoints:
    login: true
    register: true
    me: true
    setup: true

enums:
  Role: [Admin, Author, Reader]
  PostStatus: [DRAFT, REVIEW, PUBLISHED, ARCHIVED]

entities:
  User:
    features: [audit]
    fields:
      id: uuid [primary]
      email: string [required, unique, email]
      password: string [required, hidden]
      name: string [required, max:100]
      role: enum(Role) [required, default:Reader]
    permissions:
      read: ["*"]
      create: ["*"]
      update: ["@Owner", Admin]
      delete: [Admin]

  Post:
    features: [audit, soft_delete]
    fields:
      id: uuid [primary]
      title: string [required, min:2, max:200]
      slug: string [required, unique]
      body: text [required]
      status: enum(PostStatus) [default:DRAFT]
      publishedAt: datetime [optional]
      authorId: relation(User) [required, on_delete:restrict]
      tags: relation(Tag) [many]
    permissions:
      read: ["*"]
      create: [Author, Admin]
      update: ["@Owner", Admin]
      delete: ["@Owner", Admin]

  Comment:
    features: [audit]
    fields:
      id: uuid [primary]
      body: text [required, min:2]
      authorId: relation(User) [required, on_delete:cascade]
      postId: relation(Post) [required, on_delete:cascade]
    permissions:
      read: ["*"]
      create: [Reader, Author, Admin]
      update: ["@Owner", Admin]
      delete: ["@Owner", Admin]

  Tag:
    fields:
      id: uuid [primary]
      name: string [required, unique, min:2, max:50]
      slug: string [required, unique]
    permissions:
      read: ["*"]
      create: [Admin]
      update: [Admin]
      delete: [Admin]
`
