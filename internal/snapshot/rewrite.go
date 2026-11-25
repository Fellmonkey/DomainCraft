package snapshot

// Content-rewrite helpers used by the migration engine in --prune mode. When an
// entity, a field or the project (root namespace) is renamed, the developer-owned
// (overwrite: false) files that reference the old identifier are not regenerated.
// Rather than leaving a compile error, these functions rewrite the old identifier
// to the new one across the common casing forms (Pascal, camel, UPPER and, for
// entities, singular + plural). The logic is language-agnostic: it operates on
// plain identifier tokens, so a "ProductService" in C#, a "product_service" in
// Python or a bare `product` in TS are all rewritten by the same rules.

import (
	"cmp"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/DomainCraft/DomainCraft/internal/fsutil"
	"github.com/DomainCraft/DomainCraft/internal/specmeta"
	"github.com/DomainCraft/DomainCraft/pkg/textutil"
)

// rewriteCasings replaces old identifiers with new in a single pass. Variants
// are tried longest-first (so `Products` is consumed as a whole before its
// singular head `Product` gets a chance), then alphabetically for determinism.
// After a match, scanning resumes after the ORIGINAL token, so replacement text
// is never rescanned: renaming Product -> ProductItem cannot turn `Products`
// into `ProductItemItems`.
func rewriteCasings(content []byte, variants map[string]string) []byte {
	return replaceLongest(content, variants, tokenOK)
}

// replaceLongest scans content once, decoding it as runes so non-ASCII
// identifiers are handled correctly. At each position the longest matching
// variant that passes ok is applied; unmatched runes are copied verbatim.
func replaceLongest(content []byte, variants map[string]string, ok func([]rune, int, int) bool) []byte {
	if len(variants) == 0 {
		return content
	}
	type variant struct {
		old   []rune
		value []rune
	}
	vars := make([]variant, 0, len(variants))
	for old, value := range variants {
		if old == "" || old == value {
			continue
		}
		vars = append(vars, variant{old: []rune(old), value: []rune(value)})
	}
	slices.SortFunc(vars, func(a, b variant) int {
		if c := cmp.Compare(len(b.old), len(a.old)); c != 0 {
			return c
		}
		return cmp.Compare(string(a.old), string(b.old))
	})

	rs := []rune(string(content))
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); {
		matched := false
		for _, v := range vars {
			if i+len(v.old) > len(rs) {
				continue
			}
			if !hasPrefixRunes(rs[i:], v.old) {
				continue
			}
			if !ok(rs, i, len(v.old)) {
				continue
			}
			out = append(out, v.value...)
			i += len(v.old) // replacement text is never scanned again
			matched = true
			break
		}
		if !matched {
			out = append(out, rs[i])
			i++
		}
	}
	return []byte(string(out))
}

func hasPrefixRunes(s, prefix []rune) bool {
	if len(prefix) > len(s) {
		return false
	}
	for i, r := range prefix {
		if s[i] != r {
			return false
		}
	}
	return true
}

// tokenOK reports whether rs[idx:idx+length] is a standalone identifier
// occurrence. It begins at a word boundary (possibly after a single uppercase
// marker prefix such as `I` in `IProductService`, so `myProduct` stays
// untouched) and its following rune, when it is an identifier character, must
// not be a lowercase letter (so `Productivity` survives). Rune decoding keeps
// this correct for non-ASCII identifiers.
func tokenOK(rs []rune, idx, length int) bool {
	if idx > 0 {
		prev := rs[idx-1]
		prefixOK := unicode.IsUpper(prev) && (idx < 2 || !isIdentRune(rs[idx-2]))
		if isIdentRune(prev) && !prefixOK {
			return false
		}
	}
	j := idx + length
	if j < len(rs) {
		next := rs[j]
		if isIdentRune(next) && unicode.IsLower(next) {
			return false
		}
	}
	return true
}

func isIdentRune(r rune) bool {
	return specmeta.IsIdentifierRune(r)
}

// casingVariants builds the one casing-variant table used by every rename
// rewrite (content and paths). Callers pass the base form they need: PascalCase
// for entities and fields, the declared identifier for a project namespace.
// withPlural adds the plural forms on top of Pascal / camel / UPPER.
func casingVariants(oldName, newName string, withPlural bool) map[string]string {
	variants := map[string]string{
		oldName:                     newName,                     // Product -> Item
		textutil.CamelCase(oldName): textutil.CamelCase(newName), // product -> item
		strings.ToUpper(oldName):    strings.ToUpper(newName),    // PRODUCT -> ITEM
	}
	if withPlural {
		oldPlural := textutil.Pluralize(oldName)
		newPlural := textutil.Pluralize(newName)
		variants[oldPlural] = newPlural                                         // Products -> Items
		variants[textutil.CamelCase(oldPlural)] = textutil.CamelCase(newPlural) // products -> items
		variants[strings.ToUpper(oldPlural)] = strings.ToUpper(newPlural)       // PRODUCTS -> ITEMS
	}
	return variants
}

// entityCasings builds the casing-variant table for an entity rename.
func entityCasings(oldName, newName string) map[string]string {
	return casingVariants(textutil.PascalCase(oldName), textutil.PascalCase(newName), true)
}

// fieldCasings builds the casing-variant table for a field rename.
func fieldCasings(oldField, newField string) map[string]string {
	return casingVariants(textutil.PascalCase(oldField), textutil.PascalCase(newField), false)
}

// namespaceCasings builds the casing-variant table for a root-namespace rename.
func namespaceCasings(oldNs, newNs string) map[string]string {
	return casingVariants(oldNs, newNs, false)
}

// RewriteEntityContent rewrites identifier references to an old entity name back
// to the new one (Pascal / camel / UPPER / plural forms).
func RewriteEntityContent(content []byte, oldName, newName string) []byte {
	return rewriteCasings(content, entityCasings(oldName, newName))
}

// RewriteFieldContent rewrites identifier references to an old field back to the
// new one (Pascal / camel / UPPER forms).
func RewriteFieldContent(content []byte, oldField, newField string) []byte {
	return rewriteCasings(content, fieldCasings(oldField, newField))
}

// RewriteNamespaceContent rewrites the old root-namespace identifier to the new one.
func RewriteNamespaceContent(content []byte, oldNs, newNs string) []byte {
	return rewriteCasings(content, namespaceCasings(oldNs, newNs))
}

// EntityTransform returns a transform rewriting an old entity identifier to its
// new name. It is the func([]byte) shape used to build compound transforms.
func EntityTransform(oldName, newName string) func([]byte) []byte {
	return func(b []byte) []byte { return RewriteEntityContent(b, oldName, newName) }
}

// NamespaceTransform returns a transform rewriting the old root namespace.
func NamespaceTransform(n *NamespaceMismatch) func([]byte) []byte {
	if n == nil {
		return func(b []byte) []byte { return b }
	}
	oldNs, newNs := n.OldNamespace, n.NewNamespace
	return func(b []byte) []byte { return RewriteNamespaceContent(b, oldNs, newNs) }
}

// FieldTransform returns a transform rewriting a renamed field's identifiers.
func FieldTransform(fr FieldRename) func([]byte) []byte {
	oldField, newField := fr.OldField, fr.NewField
	return func(b []byte) []byte { return RewriteFieldContent(b, oldField, newField) }
}

// RenameTransforms builds the content transforms to apply after an entity rename
// oldName -> newName: the entity identifiers themselves, the project namespace
// (when the project was renamed) and any field renames declared for that entity.
func RenameTransforms(oldName, newName string, d *Diff) []func([]byte) []byte {
	transforms := []func([]byte) []byte{EntityTransform(oldName, newName)}
	if d == nil {
		return transforms
	}
	if d.NamespaceRename != nil {
		transforms = append(transforms, NamespaceTransform(d.NamespaceRename))
	}
	for i := range d.FieldRenames {
		fr := d.FieldRenames[i]
		if fr.Entity != newName {
			continue
		}
		transforms = append(transforms, FieldTransform(fr))
	}
	return transforms
}

// RewriteFile applies content transforms to the file at relPath (relative to
// outputDir), writing it back atomically only when something changed. It returns
// whether a write occurred. A missing file is not an error.
func RewriteFile(outputDir, relPath string, transforms ...func([]byte) []byte) (bool, error) {
	if len(transforms) == 0 {
		return false, nil
	}
	root, rel, err := openRootFor(outputDir, relPath)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(rel)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	out := data
	for _, t := range transforms {
		out = t(out)
	}
	if string(out) == string(data) {
		return false, nil
	}
	if err := fsutil.AtomicWriteRoot(root, rel, out); err != nil {
		return false, err
	}
	return true, nil
}
