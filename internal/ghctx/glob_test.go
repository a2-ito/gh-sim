package ghctx

import (
	"testing"

	"github.com/a2-ito/gh-sim/internal/trigger"
)

// TestMatchGlob は hashFiles のパターン構文を検証する。
func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern string
		target  string
		want    bool
	}{
		// **/ は0階層以上。ルート直下のファイルにも一致する。
		{"**/go.sum", "go.sum", true},
		{"**/go.sum", "sub/go.sum", true},
		{"**/go.sum", "a/b/c/go.sum", true},
		{"**/go.sum", "go.mod", false},
		{"**/package-lock.json", "package-lock.json", true},

		// ** はパスの区切りを跨ぐ。
		{"**", "a", true},
		{"**", "a/b/c", true},
		{"src/**", "src/a/b.go", true},
		{"src/**", "lib/a.go", false},
		{"**.go", "main.go", true},
		{"**.go", "src/main.go", true},

		// * はスラッシュを跨がない。
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", false},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false},

		// ? は任意の1文字。フィルタ側の量指定子とは意味が違う。
		{"go.su?", "go.sum", true},
		{"go.su?", "go.su", false},
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},

		// 文字クラス。
		{"v[12].txt", "v1.txt", true},
		{"v[12].txt", "v3.txt", false},

		// 完全一致。
		{"go.mod", "go.mod", true},
		{"go.mod", "go.mods", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.target, func(t *testing.T) {
			got, err := matchGlob(tt.pattern, tt.target)
			if err != nil {
				t.Fatalf("照合に失敗しました: %v", err)
			}
			if got != tt.want {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
			}
		})
	}
}

// TestMatchGlob_DiffersFromFilterPattern は hashFiles のパターンと
// ワークフローのフィルタパターンで解釈が異なることを明示する。
// 同じ構文だと思って実装を共有すると判定がずれる。
func TestMatchGlob_DiffersFromFilterPattern(t *testing.T) {
	cases := []struct {
		pattern    string
		target     string
		wantGlob   bool
		wantFilter bool
		note       string
	}{
		{
			pattern: "**/go.sum", target: "go.sum",
			wantGlob: true, wantFilter: false,
			note: "glob の **/ は0階層以上だが、フィルタの ** は文字列としての一致",
		},
		{
			pattern: "go.su?", target: "go.sum",
			wantGlob: true, wantFilter: false,
			note: "glob の ? は任意の1文字、フィルタの ? は直前の要素の0個か1個",
		},
		{
			pattern: "go.su?", target: "go.su",
			wantGlob: false, wantFilter: true,
			note: "同上。フィルタでは m が0個でも一致する",
		},
	}
	for _, c := range cases {
		t.Run(c.note, func(t *testing.T) {
			gotGlob, err := matchGlob(c.pattern, c.target)
			if err != nil {
				t.Fatalf("glob の照合に失敗しました: %v", err)
			}
			gotFilter, err := trigger.MatchPattern(c.pattern, c.target)
			if err != nil {
				t.Fatalf("フィルタの照合に失敗しました: %v", err)
			}
			if gotGlob != c.wantGlob {
				t.Errorf("glob: matchGlob(%q, %q) = %v, want %v", c.pattern, c.target, gotGlob, c.wantGlob)
			}
			if gotFilter != c.wantFilter {
				t.Errorf("フィルタ: MatchPattern(%q, %q) = %v, want %v", c.pattern, c.target, gotFilter, c.wantFilter)
			}
		})
	}
}

// TestMatchGlobList は ! による除外を確かめる。
func TestMatchGlobList(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		target   string
		want     bool
	}{
		{"肯定のみ", []string{"**/*.go"}, "src/main.go", true},
		{"除外に該当", []string{"**/*.go", "!vendor/**"}, "vendor/a/b.go", false},
		{"除外に該当しない", []string{"**/*.go", "!vendor/**"}, "src/main.go", true},
		{"複数の肯定", []string{"**/go.sum", "**/go.mod"}, "go.mod", true},
		{"どれにも一致しない", []string{"**/go.sum"}, "main.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := matchGlobList(tt.patterns, tt.target)
			if err != nil {
				t.Fatalf("照合に失敗しました: %v", err)
			}
			if got != tt.want {
				t.Errorf("matchGlobList(%v, %q) = %v, want %v", tt.patterns, tt.target, got, tt.want)
			}
		})
	}
}

func TestMatchGlob_Errors(t *testing.T) {
	if _, err := matchGlob("a[bc", "abc"); err == nil {
		t.Error("閉じられていない [ はエラーになるべきです")
	}
	if _, err := matchGlob(`abc\`, "abc"); err == nil {
		t.Error("末尾の \\ はエラーになるべきです")
	}
}
