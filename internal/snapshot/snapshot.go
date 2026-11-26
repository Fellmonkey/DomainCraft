// Package snapshot implements the schema snapshot / migration engine.
//
// After a successful `domaincraft generate` the current state of the domain
// (the IR) is persisted to `<output>/.domaincraft/snapshot.json`. On the next
// run the new domain.yaml is compared against that snapshot and a Diff is
// computed: deleted entities, renamed entities (via the `old_name` hint), and
// field type changes. The CLI uses the diff to offer cleanup of orphaned files
// (delete/rename) and to warn about custom files (overwrite: false) that may
// have been broken by a type change.
package snapshot

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/ir"
	"github.com/DomainCraft/DomainCraft/internal/renderer"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

const (
	// FormatVersion is bumped when the on-disk snapshot shape changes.
	FormatVersion = 1

	// DirName is the hidden directory storing DomainCraft state inside the output dir.
	DirName = ".domaincraft"

	// FileName is the snapshot file name inside DirName.
	FileName = "snapshot.json"
)

// EntityState records the shape of one entity at snapshot time so that later
// runs can diff it against the current IR.
type EntityState struct {
	OldName string            `json:"old_name,omitempty"`
	Fields  map[string]string `json:"fields"` // field name -> IR database type
	// Columns carries the canonical DB column name for every field. It is the
	// snapshot's copy of ir.Field.ColumnName(), so a later diff never re-derives
	// column names from logical field names: a relation field's column is the
	// FK column (category -> category_id), not the field name. Absent in
	// snapshots written before this field existed; callers fall back to the
	// old snake_case rule in that case.
	Columns map[string]string `json:"columns,omitempty"` // field name -> DB column name
	// FieldOldNames records the field-level `old_name` rename hints (previous
	// field name) declared in the CURRENT model, so the next diff can recognize
	// a safe column rename instead of a destructive drop + add.
	FieldOldNames map[string]string `json:"field_old_names,omitempty"` // field name -> previous field name
}

// Snapshot is the persisted history of the domain model for one output dir.
type Snapshot struct {
	FormatVersion    int                     `json:"format_version"`
	Bridge           string                  `json:"bridge"`
	ProjectNamespace string                  `json:"project_namespace,omitempty"` // pascal-cased project name used as the C#/other root namespace
	Files            []renderer.RenderedFile `json:"files"`
	Entities         map[string]EntityState  `json:"entities"`
}

// Path returns the absolute path of the snapshot for an output dir.
func Path(outputDir string) string {
	return filepath.Join(outputDir, DirName, FileName)
}

// Load reads a snapshot from path. It returns (nil, nil) when the snapshot
// does not exist yet (first generation run).
func Load(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the output-rooted snapshot.json
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	// Fail closed on any version we did not write ourselves. A snapshot with
	// format_version 0 predates the field: its shape is unknown, and treating
	// it as current could make `--prune` delete custom files or run DB
	// migrations against a model the developer never declared. Future versions
	// (and negative values) are equally untrusted. Regenerating the project
	// from scratch is the only safe recovery.
	if snap.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("incompatible snapshot %s: expected format_version %d, got %d", path, FormatVersion, snap.FormatVersion)
	}
	return &snap, nil
}

// Save persists the snapshot to path, creating the parent directory.
func Save(path string, snap *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // generated project dirs must stay world-readable
		return fmt.Errorf("create snapshot dir: %w", err)
	}
	// Atomic write: a crash mid-save must leave the previous snapshot intact.
	// A truncated snapshot could make the next run compute destructive
	// migrations against a model the developer never declared. No timestamp is
	// persisted, so two runs on the same model produce byte-identical files.
	if err := fsutil.WriteJSONWithNewline(path, snap, true); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}
	return nil
}

// New builds a fresh snapshot from the current IR and the renderer file manifest.
func New(bridge string, project *ir.Project, files []renderer.RenderedFile) *Snapshot {
	entities := make(map[string]EntityState, len(project.Entities))
	for _, e := range project.Entities {
		fields := make(map[string]string, len(e.Fields))
		columns := make(map[string]string, len(e.Fields))
		fieldOldNames := make(map[string]string)
		for _, f := range e.Fields {
			fields[f.Name] = f.DatabaseType
			columns[f.Name] = f.ColumnName()
			if f.OldName != "" {
				fieldOldNames[f.Name] = f.OldName
			}
		}
		entities[e.Name] = EntityState{OldName: e.OldName, Fields: fields, Columns: columns, FieldOldNames: fieldOldNames}
	}
	return &Snapshot{
		FormatVersion:    FormatVersion,
		Bridge:           bridge,
		ProjectNamespace: textutil.PascalCase(project.Name),
		Files:            files,
		Entities:         entities,
	}
}

// FileRef identifies an orphaned file relative to the output dir.
type FileRef struct {
	Path   string // relative path (forward slashes)
	Custom bool   // true when the file was generated with overwrite: false (developer-owned)
}

// DeletedEntity reports an entity that existed in the previous snapshot but is
// gone from the current domain model.
type DeletedEntity struct {
	Name  string
	Files []FileRef // entity-scoped files (relative to output dir) that still exist on disk
}

// Rename reports an entity renamed via the `old_name` hint.
type Rename struct {
	OldName string
	NewName string
	Files   []FileRef // entity-scoped files (relative to output dir) generated under the old name
}

// TypeChange reports a field whose database type changed between runs.
type TypeChange struct {
	Entity      string // current entity name
	Field       string
	OldType     string
	NewType     string
	CustomFiles []string // custom (overwrite: false) file paths for the entity that still exist
}

// FieldRename reports a field renamed via the field-level `old_name` modifier.
// A safe column rename (RenameColumn) preserves the data on a real database,
// whereas a DropColumn + AddColumn would destroy it.
type FieldRename struct {
	Entity      string // current entity name
	OldField    string // previous field name (the old_name hint)
	NewField    string // current field name
	OldColumn   string // previous snake_case DB column
	NewColumn   string // current snake_case DB column
	OldType     string
	NewType     string
	CustomFiles []string // custom (overwrite: false) file paths for the entity that still exist
	// Chained is true when the previous field name was itself a rename in the
	// prior snapshot, i.e. the developer renamed a field again before applying the
	// previous rename's migration. The DB still holds the ORIGINAL column, so the
	// generated rename target may not exist yet — the report warns about this.
	Chained bool
}

// NamespaceMismatch reports custom (overwrite: false) files that still reference
// the previous project's root namespace after the project was renamed. The bridge
// regenerates only the generated (overwrite: true) files with the new namespace;
// developer-owned files keep their old `namespace OldProject.*` and would no longer
// compile. The files listed still exist on disk and contain the old namespace token.
type NamespaceMismatch struct {
	OldNamespace string   // previous pascal-cased project name
	NewNamespace string   // current pascal-cased project name
	Files        []string // relative paths of custom files still containing the old namespace
}

// Diff is the computed difference between the old snapshot and the current IR.
type Diff struct {
	Deleted         []DeletedEntity
	Renamed         []Rename
	TypeChanges     []TypeChange
	FieldRenames    []FieldRename
	NamespaceRename *NamespaceMismatch
}

// hasEntityChanges is the single definition of "the model changed", shared by
// IsEmpty and HasSchemaChanges so adding a new change kind cannot update one
// and forget the other.
func (d *Diff) hasEntityChanges() bool {
	return len(d.Deleted) > 0 || len(d.Renamed) > 0 || len(d.FieldRenames) > 0 || len(d.TypeChanges) > 0
}

// IsEmpty reports whether the diff contains nothing actionable.
func (d *Diff) IsEmpty() bool {
	return !d.hasEntityChanges() && d.NamespaceRename == nil
}

// HasSchemaChanges reports whether the diff contains changes that must reach the
// database (deleted/renamed entities, field renames or type changes). Used to
// decide whether `domaincraft generate --prune` should run the bridge's migration
// commands automatically.
func (d *Diff) HasSchemaChanges() bool { return d.hasEntityChanges() }

// entityIndex is the shared old/new name resolution for the diff passes and
// the migration plan. A rename hint (old_name) maps an old entity to its new
// name; byName holds the current entities.
type entityIndex struct {
	byName    map[string]ir.Entity
	byOldName map[string]string // old name -> new name
}

func newEntityIndex(project *ir.Project) entityIndex {
	idx := entityIndex{
		byName:    make(map[string]ir.Entity, len(project.Entities)),
		byOldName: make(map[string]string),
	}
	for _, e := range project.Entities {
		idx.byName[e.Name] = e
		if e.OldName != "" {
			idx.byOldName[e.OldName] = e.Name
		}
	}
	return idx
}

// resolve returns the current entity and name corresponding to an old entity
// name, honouring a rename hint.
func (idx entityIndex) resolve(oldName string) (ir.Entity, string, bool) {
	newName := oldName
	if mapped, ok := idx.byOldName[oldName]; ok {
		newName = mapped
	}
	e, ok := idx.byName[newName]
	return e, newName, ok
}

// fieldsByName is the single field->database-type projection used by the
// diff and the migration plan.
func fieldsByName(e *ir.Entity) map[string]string {
	fields := make(map[string]string, len(e.Fields))
	for _, f := range e.Fields {
		fields[f.Name] = f.DatabaseType
	}
	return fields
}

// ComputeDiff compares an old snapshot against the current IR project.
// File lists are filtered to those that still exist on disk under outputDir.
func ComputeDiff(old *Snapshot, project *ir.Project, outputDir string, warn ...func(string, ...any)) *Diff {
	warnf := func(format string, args ...any) {
		for _, w := range warn {
			if w != nil {
				w(format, args...)
			}
		}
	}
	diff := &Diff{}
	if old == nil || project == nil {
		return diff
	}

	idx := newEntityIndex(project)

	// Group old entity-scoped files by entity name. The custom-file path lists
	// are derived here once, so neither change pass has to rescan the manifest.
	filesByEntity := make(map[string][]renderer.RenderedFile)
	for _, f := range old.Files {
		if f.Entity != "" {
			filesByEntity[f.Entity] = append(filesByEntity[f.Entity], f)
		}
	}
	// Stat each entity-scoped file once, not once per diff pass. The custom-file
	// list is derived from the same existing refs, so renamed/deleted/type/field
	// passes all share one on-disk snapshot without changing their contents.
	existingRefsByEntity := make(map[string][]FileRef, len(filesByEntity))
	existingCustomByEntity := make(map[string][]string, len(filesByEntity))
	for entityName, files := range filesByEntity {
		refs := existingFileRefs(outputDir, files, warnf)
		existingRefsByEntity[entityName] = refs
		custom := make([]string, 0, len(refs))
		for _, ref := range refs {
			if ref.Custom {
				custom = append(custom, ref.Path)
			}
		}
		existingCustomByEntity[entityName] = custom
	}

	diff.computeEntityMoves(old, idx, existingRefsByEntity)
	diff.computeTypeChanges(old, idx, existingCustomByEntity)

	// Field renames (via the field-level `old_name` modifier). Detected when the
	// current field declares a previous name that exists in the old snapshot and
	// the current field name did not exist there (a true rename, not a shadow).
	// A safe RenameColumn preserves the data — a Drop+Add would destroy it.
	diff.computeFieldRenames(old, idx, existingCustomByEntity)

	// Project rename: custom (overwrite: false) files carry the old root namespace
	// because they are scaffolded once. Detect them by scanning their content so the
	// developer gets an actionable list instead of a compile error later.
	currentNamespace := textutil.PascalCase(project.Name)
	if old.ProjectNamespace != "" && old.ProjectNamespace != currentNamespace {
		diff.NamespaceRename = detectNamespaceMismatch(outputDir, old, old.ProjectNamespace, currentNamespace, warnf)
	}

	return diff
}

// computeEntityMoves collects renamed and deleted entities from one rename-aware
// index, instead of resolving old names in two independent passes.
func (d *Diff) computeEntityMoves(old *Snapshot, idx entityIndex, refs map[string][]FileRef) {
	for oldName, newName := range idx.byOldName {
		if _, ok := old.Entities[oldName]; !ok {
			continue
		}
		d.Renamed = append(d.Renamed, Rename{OldName: oldName, NewName: newName, Files: refs[oldName]})
	}
	slices.SortFunc(d.Renamed, func(a, b Rename) int { return cmp.Compare(a.OldName, b.OldName) })

	for oldName := range old.Entities {
		if _, ok := idx.byName[oldName]; ok {
			continue
		}
		if _, renamed := idx.byOldName[oldName]; renamed {
			continue
		}
		d.Deleted = append(d.Deleted, DeletedEntity{Name: oldName, Files: refs[oldName]})
	}
	slices.SortFunc(d.Deleted, func(a, b DeletedEntity) int { return cmp.Compare(a.Name, b.Name) })
}

// computeTypeChanges reports fields whose database type changed in entities
// that still exist (possibly under a new name).
func (d *Diff) computeTypeChanges(old *Snapshot, idx entityIndex, custom map[string][]string) {
	for oldName, oldState := range old.Entities {
		newEntity, newName, ok := idx.resolve(oldName)
		if !ok {
			continue
		}
		newFields := fieldsByName(&newEntity)
		customFiles := custom[oldName]
		for fieldName, oldType := range oldState.Fields {
			newType, ok := newFields[fieldName]
			if !ok || oldType == newType {
				continue
			}
			d.TypeChanges = append(d.TypeChanges, TypeChange{
				Entity:      newName,
				Field:       fieldName,
				OldType:     oldType,
				NewType:     newType,
				CustomFiles: customFiles,
			})
		}
	}
	slices.SortFunc(d.TypeChanges, func(a, b TypeChange) int {
		if c := cmp.Compare(a.Entity, b.Entity); c != 0 {
			return c
		}
		return cmp.Compare(a.Field, b.Field)
	})
}

// computeFieldRenames reports field-level old_name hints that describe a safe
// rename rather than a drop+add.
func (d *Diff) computeFieldRenames(old *Snapshot, idx entityIndex, custom map[string][]string) {
	for oldName, oldState := range old.Entities {
		newEntity, newName, ok := idx.resolve(oldName)
		if !ok {
			continue
		}
		customFiles := custom[oldName]
		for _, f := range newEntity.Fields {
			if f.OldName == "" {
				continue
			}
			oldFieldType, wasPresent := oldState.Fields[f.OldName]
			if !wasPresent {
				continue
			}
			// Ambiguous: the new name already existed before — not a clean rename.
			if _, already := oldState.Fields[f.Name]; already {
				continue
			}
			oldColumn := oldState.Columns[f.OldName]
			if oldColumn == "" {
				oldColumn = f.OldColumnName()
			}
			d.FieldRenames = append(d.FieldRenames, FieldRename{
				Entity: newName, OldField: f.OldName, NewField: f.Name,
				OldColumn: oldColumn, NewColumn: f.ColumnName(),
				OldType: oldFieldType, NewType: f.DatabaseType,
				CustomFiles: customFiles,
				Chained:     oldState.FieldOldNames[f.OldName] != "",
			})
		}
	}
	slices.SortFunc(d.FieldRenames, func(a, b FieldRename) int {
		if c := cmp.Compare(a.Entity, b.Entity); c != 0 {
			return c
		}
		return cmp.Compare(a.OldField, b.OldField)
	})
}

// detectNamespaceMismatch finds custom files from the old snapshot (still present
// on disk) whose content references the old root namespace.
func detectNamespaceMismatch(outputDir string, old *Snapshot, oldNamespace, newNamespace string, warn func(string, ...any)) *NamespaceMismatch {
	var affected []string
	// The same on-disk filter as every other diff pass: one Stat per file,
	// unsafe paths and disappeared files reported through the shared warning.
	for _, ref := range existingFileRefs(outputDir, old.Files, warn) {
		if !ref.Custom {
			continue
		}
		abs, err := safeRelPath(outputDir, ref.Path)
		if err != nil {
			warn("skipping custom file %q while checking the namespace: %v", ref.Path, err)
			continue
		}
		data, err := os.ReadFile(abs) //nolint:gosec // abs passed safeRelPath validation
		if err != nil {
			warn("cannot read custom file %q while checking the namespace: %v", ref.Path, err)
			continue
		}
		if referencesNamespace(string(data), oldNamespace) {
			affected = append(affected, ref.Path)
		}
	}
	if len(affected) == 0 {
		return nil
	}
	slices.Sort(affected)
	return &NamespaceMismatch{OldNamespace: oldNamespace, NewNamespace: newNamespace, Files: affected}
}

// referencesNamespace reports whether s mentions the old root namespace as a
// standalone identifier. It deliberately does not assume C# syntax: the same
// token appears in `namespace X;`, TypeScript imports and Python module paths.
// The shared identifier-aware rewriter supplies the boundary rules.
func referencesNamespace(s, namespace string) bool {
	if namespace == "" {
		return false
	}
	found := false
	// The same identifier-aware scanner as the rewrites, but with an early
	// stop: no sentinel replacement and no full copy of the file content.
	replaceLongest([]byte(s), casingVariants(namespace, namespace+"__", false), tokenOK, func([]byte) bool {
		found = true
		return true
	})
	return found
}

// NamespaceRenameReport returns a human-readable report for the project-namespace
// mismatch in the diff, or an empty string when there is none.
func (d *Diff) NamespaceRenameReport() string {
	n := d.NamespaceRename
	if n == nil || len(n.Files) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "You renamed the project from %q to %q. Developer-owned custom files\n", n.OldNamespace, n.NewNamespace)
	b.WriteString("(overwrite: false) still declare the old root namespace and will no longer compile:\n\n")
	for i, f := range n.Files {
		fmt.Fprintf(&b, "%d. %s\n", i+1, f)
	}
	b.WriteString("\nReplace `namespace " + n.OldNamespace + ".` with `namespace " + n.NewNamespace + ".` in each file ")
	b.WriteString("(or run a find-and-replace across your project).\n")
	return b.String()
}

// existingItems is the single safeRelPath + Stat + skip + sort filter used by
// every caller that turns snapshot-supplied paths into on-disk facts. Items
// whose path escapes outputDir or is missing are dropped; the rest stay in
// deterministic path order.
func existingItems[T any](outputDir string, items []T, relPath func(T) string, warn func(string, ...any)) []T {
	var result []T
	for _, item := range items {
		path := relPath(item)
		abs, err := safeRelPath(outputDir, path)
		if err != nil {
			warn("skipping manifest file %q during migration diff: %v", path, err)
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			warn("manifest file %q is no longer on disk: %v", path, err)
			continue
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b T) int { return cmp.Compare(relPath(a), relPath(b)) })
	return result
}

// existingFileRefs filters old entity-scoped files to those that still exist
// on disk, preserving whether each is a custom (overwrite: false) file.
func existingFileRefs(outputDir string, files []renderer.RenderedFile, warn func(string, ...any)) []FileRef {
	existing := existingItems(outputDir, files, func(f renderer.RenderedFile) string { return f.Path }, warn)
	refs := make([]FileRef, 0, len(existing))
	for _, f := range existing {
		refs = append(refs, FileRef{Path: f.Path, Custom: f.Custom})
	}
	return refs
}

// RenameRelPath maps an entity-scoped file path to its path after the entity
// rename oldName -> newName. It uses the same casing-variant table as content
// rewrites, so singular/plural paths in any casing are renamed consistently.
func RenameRelPath(relPath, oldName, newName string) string {
	// Single pass, longest match first. Two sequential ReplaceAll calls would
	// rescan the replacement of the plural form and rewrite the singular head in
	// it: Product -> ProductItem would turn Products into ProductItemItems.
	variants := casingVariants(oldName, newName, true)
	return string(replaceLongest([]byte(relPath), variants, tokenOK))
}

// DeleteFile removes an entity-scoped file (path relative to outputDir).
func DeleteFile(outputDir, relPath string) error {
	return withRoot(outputDir, relPath, func(root *os.Root, rel string) error {
		if err := root.Remove(rel); err != nil {
			// The diff only includes files that existed at scan time; another
			// run may have removed one since. Treat that as the end state.
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("remove %s: %w", relPath, err)
		}
		return nil
	})
}

// RenameEntityFile renames an entity-scoped file after the entity rename
// oldName -> newName. It returns the new relative path and whether the file was
// actually renamed. When the destination already exists (e.g. it was generated
// this run or created manually) the file is left untouched.
func RenameEntityFile(outputDir, relPath, oldName, newName string) (newRel string, renamed bool, err error) {
	newRel = RenameRelPath(relPath, oldName, newName)
	if newRel == relPath {
		return newRel, false, nil
	}
	err = withRoot(outputDir, relPath, func(srcRoot *os.Root, srcRel string) error {
		return withRoot(outputDir, newRel, func(dstRoot *os.Root, dstRel string) error {
			if _, err := dstRoot.Lstat(dstRel); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("check destination %s: %w", newRel, err)
			}
			if err := dstRoot.MkdirAll(filepath.Dir(dstRel), 0o755); err != nil {
				return fmt.Errorf("create parent dir for %s: %w", newRel, err)
			}
			// Re-check immediately before the rename: a file created between
			// the first check and here would otherwise be overwritten.
			if _, err := dstRoot.Lstat(dstRel); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("check destination %s: %w", newRel, err)
			}
			if err := srcRoot.Rename(srcRel, dstRel); err != nil {
				return fmt.Errorf("rename %s to %s: %w", relPath, newRel, err)
			}
			renamed = true
			return nil
		})
	})
	return newRel, renamed, err
}

// removeFileFn is a seam for testing the failure path of ReplaceScaffoldedFile.
var removeFileFn = func(root *os.Root, name string) error { return root.Remove(name) }

// ReplaceScaffoldedFile replaces a scaffolded custom file at destRel with the
// developer's existing custom file at sourceRel, applying transforms to its
// content in memory first. The destination is committed atomically before the
// source is removed, so a failed write or transform never destroys either copy.
// A failed source removal is reported: the destination is already correct and
// the developer can remove the leftover manually.
func ReplaceScaffoldedFile(outputDir, sourceRel, destRel string, transforms ...func([]byte) []byte) error {
	return withRoot(outputDir, sourceRel, func(srcRoot *os.Root, srcRel string) error {
		return withRoot(outputDir, destRel, func(dstRoot *os.Root, dstRel string) error {
			data, err := srcRoot.ReadFile(srcRel)
			if err != nil {
				return fmt.Errorf("read %s: %w", sourceRel, err)
			}
			out := data
			for _, transform := range transforms {
				out = transform(out)
			}
			existing, readErr := dstRoot.ReadFile(dstRel)
			switch {
			case readErr == nil && bytes.Equal(existing, out):
				// Already up to date: avoid a temp file and mtime reset.
			case readErr != nil && !errors.Is(readErr, os.ErrNotExist):
				return fmt.Errorf("read %s: %w", destRel, readErr)
			default:
				if err := fsutil.AtomicWriteRoot(dstRoot, dstRel, out); err != nil {
					return fmt.Errorf("write %s: %w", destRel, err)
				}
			}
			if err := removeFileFn(srcRoot, srcRel); err != nil {
				return fmt.Errorf("remove replaced %s: %w", sourceRel, err)
			}
			return nil
		})
	})
}

// FieldRenameReport returns a human-readable report for the field renames in
// the diff, or an empty string when there are none. It tells the developer that
// a safe column rename is required — a DropColumn + AddColumn would destroy the
// existing data. Column names are bridge-agnostic snake_case; each bridge turns
// a field's `old_name` into its own rename statement (see the C# bridge, which
// generates migrationBuilder.RenameColumn guidance).
func (d *Diff) FieldRenameReport() string {
	if len(d.FieldRenames) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("You renamed fields in domain.yaml. Keep the existing data by renaming the DB\n")
	b.WriteString("column instead of dropping it and adding a new one (that would erase the data):\n\n")
	for i, fr := range d.FieldRenames {
		fmt.Fprintf(&b, "%d. Entity: %s — field %s renamed to %s\n", i+1, fr.Entity, fr.OldField, fr.NewField)
		fmt.Fprintf(&b, "   Column:   %s -> %s\n", fr.OldColumn, fr.NewColumn)
		if fr.Chained {
			fmt.Fprintf(&b, "   WARNING:  field %s was itself the target of a rename in the previous\n", fr.OldField)
			fmt.Fprintf(&b, "             snapshot, and that rename may never have been applied. The DB still\n")
			fmt.Fprintf(&b, "             holds the ORIGINAL column, so renaming %s -> %s may fail\n", fr.OldColumn, fr.NewColumn)
			fmt.Fprintf(&b, "             because %s does not exist yet. Apply each rename's migration as\n", fr.OldColumn)
			fmt.Fprintf(&b, "             you go (docker auto-migrate, --migrate, --prune).\n")
		}
		if fr.OldType != fr.NewType {
			fmt.Fprintf(&b, "   Types:    %s -> %s (was a rename AND a type change)\n", fr.OldType, fr.NewType)
		}
		if len(fr.CustomFiles) > 0 {
			b.WriteString("   Files that may reference the old name:\n")
			for _, f := range fr.CustomFiles {
				fmt.Fprintf(&b, "   - %s\n", f)
			}
		}
	}
	b.WriteString("\nIn a single migration, rename each column (RenameColumn) instead of letting EF\n")
	b.WriteString("drop and re-add it — the generated code already writes the new column name.\n")
	b.WriteString("The C# bridge ships the exact RenameTable / RenameColumn statements in a SchemaRenames\n")
	b.WriteString("helper to call from your migration; `domaincraft generate --prune` also re-runs the\n")
	b.WriteString("bridge's migration commands (best-effort) after a rename.\n")
	return b.String()
}

// TypeChangeReport returns a human-readable refactoring report for the type
// changes in the diff.
func (d *Diff) TypeChangeReport() string {
	if len(d.TypeChanges) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("You changed data types in domain.yaml. Custom files (overwrite: false)\n")
	b.WriteString("that reference these fields may have broken and need manual refactoring:\n\n")
	for i, tc := range d.TypeChanges {
		fmt.Fprintf(&b, "%d. Entity: %s (Field: %s)\n", i+1, tc.Entity, tc.Field)
		fmt.Fprintf(&b, "   Change: %s -> %s\n", tc.OldType, tc.NewType)
		if len(tc.CustomFiles) > 0 {
			b.WriteString("   Files to check:\n")
			for _, f := range tc.CustomFiles {
				fmt.Fprintf(&b, "   - %s\n", f)
			}
		} else {
			b.WriteString("   (no custom files recorded — verify generated code in your IDE)\n")
		}
	}
	b.WriteString("\nTip: open the project in your IDE and check for compilation errors.\n")
	return b.String()
}
