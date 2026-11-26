package ir

import "testing"

func TestIRFieldTypePredicates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                           string
		dbType                         string
		isUUID, isText, isInt, isFloat bool
		isBool, isDate, isDt, isJSON   bool
		isNumeric, isEnum              bool
		enumName                       string
	}{
		{name: "uuid", dbType: "uuid", isUUID: true, enumName: "uuid"},
		{name: "string", dbType: "string", isText: true, enumName: "string"},
		{name: "text", dbType: "text", isText: true, enumName: "text"},
		{name: "int", dbType: "int", isInt: true, isNumeric: true, enumName: "int"},
		{name: "bigint", dbType: "bigint", isInt: true, isNumeric: true, enumName: "bigint"},
		{name: "float", dbType: "float", isFloat: true, isNumeric: true, enumName: "float"},
		{name: "decimal", dbType: "decimal", isFloat: true, isNumeric: true, enumName: "decimal"},
		{name: "boolean", dbType: "boolean", isBool: true, enumName: "boolean"},
		{name: "date", dbType: "date", isDate: true, enumName: "date"},
		{name: "datetime", dbType: "datetime", isDt: true, enumName: "datetime"},
		{name: "json", dbType: "json", isJSON: true, enumName: "json"},
		{name: "jsonb", dbType: "jsonb", isJSON: true, enumName: "jsonb"},
		{name: "enum_status", dbType: "Status", isEnum: true, enumName: "Status"},
		{name: "array_int", dbType: "array(int)", enumName: "int"},
		{name: "array_enum_status", dbType: "array(Status)", isEnum: true, enumName: "Status"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := Field{DatabaseType: c.dbType}
			if got := f.IsUuid(); got != c.isUUID {
				t.Errorf("%q IsUuid() = %v, want %v", c.dbType, got, c.isUUID)
			}
			if got := f.IsText(); got != c.isText {
				t.Errorf("%q IsText() = %v, want %v", c.dbType, got, c.isText)
			}
			if got := f.IsInteger(); got != c.isInt {
				t.Errorf("%q IsInteger() = %v, want %v", c.dbType, got, c.isInt)
			}
			if got := f.IsFloat(); got != c.isFloat {
				t.Errorf("%q IsFloat() = %v, want %v", c.dbType, got, c.isFloat)
			}
			if got := f.IsNumeric(); got != c.isNumeric {
				t.Errorf("%q IsNumeric() = %v, want %v", c.dbType, got, c.isNumeric)
			}
			if got := f.IsBoolean(); got != c.isBool {
				t.Errorf("%q IsBoolean() = %v, want %v", c.dbType, got, c.isBool)
			}
			if got := f.IsDate(); got != c.isDate {
				t.Errorf("%q IsDate() = %v, want %v", c.dbType, got, c.isDate)
			}
			if got := f.IsDateTime(); got != c.isDt {
				t.Errorf("%q IsDateTime() = %v, want %v", c.dbType, got, c.isDt)
			}
			if got := f.IsJson(); got != c.isJSON {
				t.Errorf("%q IsJson() = %v, want %v", c.dbType, got, c.isJSON)
			}
			if got := f.IsEnum(); got != c.isEnum {
				t.Errorf("%q IsEnum() = %v, want %v", c.dbType, got, c.isEnum)
			}
			if got := f.EnumTypeName(); got != c.enumName {
				t.Errorf("%q EnumTypeName() = %q, want %q", c.dbType, got, c.enumName)
			}
		})
	}
}

func TestIRFieldSpecificTypePredicates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                          string
		dbType                        string
		isString, isBigInt, isDecimal bool
		isJSONB                       bool
	}{
		{name: "string", dbType: "string", isString: true},
		{name: "text", dbType: "text", isString: false},
		{name: "int", dbType: "int"},
		{name: "bigint", dbType: "bigint", isBigInt: true},
		{name: "float", dbType: "float"},
		{name: "decimal", dbType: "decimal", isDecimal: true},
		{name: "json", dbType: "json"},
		{name: "jsonb", dbType: "jsonb", isJSONB: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := Field{DatabaseType: c.dbType}
			if got := f.IsString(); got != c.isString {
				t.Errorf("%q IsString() = %v, want %v", c.dbType, got, c.isString)
			}
			if got := f.IsBigInt(); got != c.isBigInt {
				t.Errorf("%q IsBigInt() = %v, want %v", c.dbType, got, c.isBigInt)
			}
			if got := f.IsDecimal(); got != c.isDecimal {
				t.Errorf("%q IsDecimal() = %v, want %v", c.dbType, got, c.isDecimal)
			}
			if got := f.IsJsonB(); got != c.isJSONB {
				t.Errorf("%q IsJsonB() = %v, want %v", c.dbType, got, c.isJSONB)
			}
		})
	}
}

func TestIRFieldIsPatchable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		f    Field
		want bool
	}{
		{"scalar", Field{Name: "title", DatabaseType: "string"}, true},
		{"single-fk", Field{Name: "order", DatabaseType: "uuid", IsRelation: true}, true},
		{"collection-fk", Field{Name: "items", DatabaseType: "uuid", IsRelation: true, IsMany: true}, false},
		{"array", Field{Name: "tags", DatabaseType: "array(string)"}, false},
		{"enum", Field{Name: "status", DatabaseType: "Status"}, false},
		{"json", Field{Name: "meta", DatabaseType: "json"}, false},
		{"primary", Field{Name: "id", DatabaseType: "uuid", IsPrimary: true}, false},
		{"readonly", Field{Name: "slug", DatabaseType: "string", IsReadonly: true}, false},
		{"sensitive", Field{Name: "password", DatabaseType: "string"}, false},
		{"feature", Field{Name: "version", DatabaseType: "int"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.f.IsPatchable(); got != c.want {
				t.Errorf("%s IsPatchable() = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestIRFieldIsTextOnly(t *testing.T) {
	t.Parallel()
	if !(Field{DatabaseType: "text"}).IsTextOnly() {
		t.Error("text should be IsTextOnly")
	}
	if (Field{DatabaseType: "string"}).IsTextOnly() {
		t.Error("string should not be IsTextOnly")
	}
}

func TestIREntityTableNameAndForeignKeyColumnName(t *testing.T) {
	t.Parallel()
	e := Entity{Name: "IPv4Rule", NamePlural: "IPv4Rules"}
	if got := e.TableName(); got != "i_pv4rules" {
		t.Errorf("TableName() = %q, want %q (acronyms must match ToDatabaseColumnName)", got, "i_pv4rules")
	}

	r := Relation{FieldName: "ipv4Address"}
	if got := r.ForeignKeyColumnName(); got != "ipv4address_id" {
		t.Errorf("ForeignKeyColumnName() = %q, want %q (must match ToDatabaseColumnName, not template snakecase)", got, "ipv4address_id")
	}
	// A field already suffixed with Id must not get a double Id.
	if got := (Relation{FieldName: "orderId"}).ForeignKeyColumnName(); got != "order_id" {
		t.Errorf("ForeignKeyColumnName(orderId) = %q, want order_id", got)
	}
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"uuid":          "uuid",
		"string":        "text",
		"text":          "text",
		"int":           "int",
		"bigint":        "bigint",
		"float":         "float",
		"decimal":       "decimal",
		"boolean":       "bool",
		"date":          "date",
		"datetime":      "datetime",
		"json":          "json",
		"jsonb":         "json",
		"Status":        "enum",
		"array(int)":    "array",
		"array(Status)": "array",
	}
	for dbType, want := range cases {
		if got := KindOf(dbType); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", dbType, got, want)
		}
	}
	if got := (SeedValue{DBType: "array(uuid)"}).Kind(); got != "array" {
		t.Errorf("SeedValue.Kind() = %q, want array", got)
	}
	if got := (SeedValue{DBType: "array(uuid)"}).ElementType(); got != "uuid" {
		t.Errorf("SeedValue.ElementType() = %q, want uuid", got)
	}
}

func TestIRPermissionsIsPublic(t *testing.T) {
	t.Parallel()
	p := &Permissions{Read: []string{"*"}, Create: []string{"Admin"}, Update: nil, Delete: []string{}}
	if !p.IsPublic("read") {
		t.Error("IsPublic(read) = false, want true")
	}
	if p.IsPublic("create") {
		t.Error("IsPublic(create) = true, want false")
	}
	if p.IsPublic("update") {
		t.Error("IsPublic(update) = true, want false")
	}
	if p.IsPublic("delete") {
		t.Error("IsPublic(delete) = true, want false")
	}
	var nilPerms *Permissions
	if nilPerms.IsPublic("read") {
		t.Error("nil IsPublic(read) = true, want false")
	}
}

func TestIREndpointAuthPolicy(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"list":   "Read",
		"get":    "Read",
		"create": "Create",
		"update": "Update",
		"patch":  "Update",
		"delete": "Delete",
	}
	for op, want := range cases {
		if got := (Endpoint{Operation: op}).AuthPolicy(); got != want {
			t.Errorf("AuthPolicy(%q) = %q, want %q", op, got, want)
		}
	}
}
