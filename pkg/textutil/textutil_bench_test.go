package textutil

import "testing"

func BenchmarkSplitIdentifier(b *testing.B) {
	for _, tc := range []struct{ name, value string }{
		{name: "simple", value: "name"},
		{name: "snake", value: "created_at"},
		{name: "camel", value: "firstName"},
		{name: "acronym", value: "HTTPServer"},
		{name: "unicode", value: "èCafé_Наименование"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = SplitIdentifier(tc.value)
			}
		})
	}
}

func BenchmarkToDatabaseColumnName(b *testing.B) {
	for _, tc := range []struct{ name, value string }{
		{name: "simple", value: "name"},
		{name: "camel", value: "IPv4Address"},
		{name: "acronym", value: "HTTPServer"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = ToDatabaseColumnName(tc.value)
			}
		})
	}
}

func BenchmarkPascalCase(b *testing.B) {
	for _, tc := range []struct{ name, value string }{
		{name: "simple", value: "name"},
		{name: "snake", value: "created_at"},
		{name: "acronym", value: "HTTPServer"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = PascalCase(tc.value)
			}
		})
	}
}
