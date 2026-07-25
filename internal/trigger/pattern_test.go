package trigger

import (
	"strings"
	"testing"
)

// TestMatchPattern_Cheatsheet は GitHub のフィルタパターン構文を検証する。
// * がスラッシュを跨がない点と、? と + が量指定子として働く点が
// 一般的な glob と異なるため、そこを重点的に確かめる。
func TestMatchPattern_Cheatsheet(t *testing.T) {
	tests := []struct {
		pattern string
		target  string
		want    bool
	}{
		// 完全一致。
		{"main", "main", true},
		{"main", "maintenance", false},
		{"main", "Main", false},
		{"releases/mona-the-octocat", "releases/mona-the-octocat", true},

		// * はスラッシュ以外の0文字以上。
		{"feature/*", "feature/my-branch", true},
		{"feature/*", "feature/your-branch", true},
		{"feature/*", "feature/", true},
		{"feature/*", "feature/beta-a/my-branch", false},
		{"*", "main", true},
		{"*", "feature/x", false},
		{"*feature", "mona-feature", true},
		{"*feature", "feature", true},
		{"*feature", "ver-10-feature", true},
		{"v2*", "v2", true},
		{"v2*", "v2.0", true},
		{"v2*", "v2.9", true},
		{"v2*", "v3.0", false},

		// ** はスラッシュを跨いで一致する。
		{"feature/**", "feature/beta-a/my-lab", true},
		{"feature/**", "feature/a/b/c", true},
		{"feature/**", "feature/my-branch", true},
		{"**", "main", true},
		{"**", "feature/a/b", true},
		{"**.js", "app.js", true},
		{"**.js", "src/lib/app.js", true},
		{"**.js", "app.ts", false},
		{"*.js", "app.js", true},
		{"*.js", "src/app.js", false},

		// 文字クラスと量指定子の組み合わせ。
		{"v[12].[0-9]+.[0-9]+", "v1.10.1", true},
		{"v[12].[0-9]+.[0-9]+", "v2.0.0", true},
		{"v[12].[0-9]+.[0-9]+", "v1.9", false},
		{"v[12].[0-9]+.[0-9]+", "v3.0.0", false},

		// ? は直前の要素の0個か1個。「任意の1文字」ではない。
		{"main?", "main", true},
		{"main?", "mai", true},
		{"main?", "mainx", false},
		{"main?", "mainn", false},
		{"v1.0?", "v1.0", true},
		{"v1.0?", "v1.", true},

		// + は直前の要素の1個以上。
		{"v1.0+", "v1.0", true},
		{"v1.0+", "v1.00", true},
		{"v1.0+", "v1.000", true},
		{"v1.0+", "v1.", false},

		// ドットは通常の文字として扱われる。
		{"v1.0", "v1.0", true},
		{"v1.0", "v1x0", false},

		// バックスラッシュでメタ文字をエスケープできる。
		{`a\*b`, "a*b", true},
		{`a\*b`, "axb", false},

		// パス向けのパターン。
		{"docs/**", "docs/readme.md", true},
		{"docs/**", "docs/a/b/c.md", true},
		{"docs/**", "src/main.go", false},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.target, func(t *testing.T) {
			got, err := MatchPattern(tt.pattern, tt.target)
			if err != nil {
				t.Fatalf("照合に失敗しました: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchPattern(%q, %q) = %v, want %v", tt.pattern, tt.target, got, tt.want)
			}
		})
	}
}

// TestMatchFilters_Negation は ! で始まるパターンが
// それより前の一致を打ち消すことを確かめる。
func TestMatchFilters_Negation(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		target   string
		want     bool
	}{
		{"肯定のみで一致", []string{"main", "develop"}, "main", true},
		{"肯定のみで不一致", []string{"main", "develop"}, "feature/x", false},
		{"否定で打ち消される", []string{"releases/**", "!releases/**-alpha"}, "releases/1.0-alpha", false},
		{"否定に該当しない", []string{"releases/**", "!releases/**-alpha"}, "releases/1.0", true},
		{"否定の後の肯定が勝つ", []string{"releases/**", "!releases/**-alpha", "releases/2.0-alpha"}, "releases/2.0-alpha", true},
		{"空のリストは不一致", nil, "main", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchFilters(tt.patterns, tt.target)
			if err != nil {
				t.Fatalf("照合に失敗しました: %v", err)
			}
			if got != tt.want {
				t.Errorf("MatchFilters(%v, %q) = %v, want %v", tt.patterns, tt.target, got, tt.want)
			}
		})
	}
}

func TestMatchPattern_Errors(t *testing.T) {
	tests := []struct {
		pattern string
		wantMsg string
	}{
		{"?abc", "? の前に対象となる要素がありません"},
		{"+abc", "+ の前に対象となる要素がありません"},
		{"a[bc", "[ が ] で閉じられていません"},
		{`abc\`, `\ の後に文字がありません`},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			_, err := MatchPattern(tt.pattern, "abc")
			if err == nil {
				t.Fatal("エラーになるべきです")
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("エラーに %q が含まれるべきです: %v", tt.wantMsg, err)
			}
		})
	}
}

// TestMatchPattern_CacheIsConsistent は同じパターンを繰り返し照合しても
// キャッシュ経由で同じ結果になることを確かめる。
func TestMatchPattern_CacheIsConsistent(t *testing.T) {
	for i := range 3 {
		got, err := MatchPattern("feature/*", "feature/x")
		if err != nil {
			t.Fatalf("照合に失敗しました: %v", err)
		}
		if !got {
			t.Fatalf("%d回目の照合結果が異なります", i+1)
		}
	}
	// エラーになるパターンもキャッシュされるが、毎回同じエラーを返す。
	for i := range 3 {
		if _, err := MatchPattern("?bad", "x"); err == nil {
			t.Fatalf("%d回目もエラーになるべきです", i+1)
		}
	}
}
