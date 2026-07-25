package expr

import (
	"reflect"
	"testing"
)

// TestUnknown_BehavesLikeNull は不明値が評価上は null と同じに
// 振る舞うことを確かめる。既存の評価規則を変えずに扱えることが前提。
func TestUnknown_BehavesLikeNull(t *testing.T) {
	u := Unknown("needs.build.outputs.version")

	if !u.IsNull() {
		t.Error("不明値は null として扱われるべきです")
	}
	if u.Truthy() {
		t.Error("不明値は偽として扱われるべきです")
	}
	if got := u.AsString(); got != "" {
		t.Errorf("AsString() = %q, want 空文字列", got)
	}
	if got := u.AsNumber(); got != 0 {
		t.Errorf("AsNumber() = %v, want 0", got)
	}
	if !Equal(u, Null()) {
		t.Error("null と等しく比較されるべきです")
	}
	if !u.IsUnknown() || u.UnknownLabel() != "needs.build.outputs.version" {
		t.Errorf("由来が保持されるべきです: %q", u.UnknownLabel())
	}

	// 通常の null は不明値ではない。
	if Null().IsUnknown() {
		t.Error("通常の null は不明値ではありません")
	}
}

// TestUnknown_IsRecordedDuringEval は評価中に参照された不明値が
// コンテキストに記録されることを確かめる。
func TestUnknown_IsRecordedDuringEval(t *testing.T) {
	newCtx := func() *Context {
		outputs := NewObject()
		outputs.Set("version", Unknown("needs.build.outputs.version"))

		build := NewObject()
		build.Set("result", String("success"))
		build.Set("outputs", Obj(outputs))

		needs := NewObject()
		needs.Set("build", Obj(build))

		ctx := NewContext()
		ctx.Set("needs", Obj(needs))
		ctx.Set("github", FromGo(map[string]any{"event_name": "push"}))
		return ctx
	}

	tests := []struct {
		src  string
		want []string
	}{
		// 不明値を参照すれば記録される。
		{"needs.build.outputs.version == '1.0'", []string{"needs.build.outputs.version"}},
		{"needs.build.outputs.version != '1.0'", []string{"needs.build.outputs.version"}},
		{"!needs.build.outputs.version", []string{"needs.build.outputs.version"}},
		{"format('{0}', needs.build.outputs.version)", []string{"needs.build.outputs.version"}},
		// 確定値だけを参照する場合は記録されない。
		{"needs.build.result == 'success'", nil},
		{"github.event_name == 'push'", nil},
		// 短絡評価で到達しなければ記録されない。
		{"false && needs.build.outputs.version == '1.0'", nil},
		{"true || needs.build.outputs.version == '1.0'", nil},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			ctx := newCtx()
			if _, err := Evaluate(tt.src, ctx); err != nil {
				t.Fatalf("評価に失敗しました: %v", err)
			}
			if !reflect.DeepEqual(ctx.Unknowns, tt.want) {
				t.Errorf("記録された不明値: got %v, want %v", ctx.Unknowns, tt.want)
			}
		})
	}
}

// TestUnknown_RecordedOnceEvenIfReferencedTwice は同じ参照が
// 複数回現れても記録が重複しないことを確かめる。
func TestUnknown_RecordedOnceEvenIfReferencedTwice(t *testing.T) {
	o := NewObject()
	o.Set("x", Unknown("needs.a.outputs.x"))
	ctx := NewContext()
	ctx.Set("needs", Obj(o))

	if _, err := Evaluate("needs.x == 'a' || needs.x == 'b'", ctx); err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	if len(ctx.Unknowns) != 1 {
		t.Errorf("記録は1件にまとまるべきです: %v", ctx.Unknowns)
	}
}

// TestUnknown_CloneDoesNotCarryRecords は Clone が記録を引き継がないことを
// 確かめる。記録は評価ごとに独立している必要がある。
func TestUnknown_CloneDoesNotCarryRecords(t *testing.T) {
	o := NewObject()
	o.Set("x", Unknown("needs.a.outputs.x"))
	ctx := NewContext()
	ctx.Set("needs", Obj(o))

	if _, err := Evaluate("needs.x", ctx); err != nil {
		t.Fatalf("評価に失敗しました: %v", err)
	}
	if len(ctx.Unknowns) != 1 {
		t.Fatalf("記録されるべきです: %v", ctx.Unknowns)
	}

	clone := ctx.Clone()
	if len(clone.Unknowns) != 0 {
		t.Errorf("複製には記録を引き継がないべきです: %v", clone.Unknowns)
	}
}

// TestObject_UnknownFallback は未知のキーへのアクセスを
// 不明値として返す設定を確かめる。
func TestObject_UnknownFallback(t *testing.T) {
	o := NewObject()
	o.Set("known", String("value"))
	o.SetUnknownFallback("steps.detect.outputs")

	// 既知のキーはそのまま返る。
	v, ok := o.Get("known")
	if !ok || v.AsString() != "value" {
		t.Errorf("既知のキーは通常どおり返るべきです: %v", v.AsString())
	}
	if v.IsUnknown() {
		t.Error("既知のキーは不明値ではありません")
	}

	// 未知のキーは不明値になり、ラベルに参照名が入る。
	v, ok = o.Get("nosuch")
	if !ok {
		t.Fatal("未知のキーでも値が返るべきです")
	}
	if !v.IsUnknown() {
		t.Error("未知のキーは不明値になるべきです")
	}
	if got := v.UnknownLabel(); got != "steps.detect.outputs.nosuch" {
		t.Errorf("ラベル: got %q", got)
	}

	// 設定していないオブジェクトでは従来どおり null。
	plain := NewObject()
	v, ok = plain.Get("nosuch")
	if ok || v.IsUnknown() {
		t.Error("設定していない場合は従来どおり null になるべきです")
	}
}

// TestObject_CloneKeepsUnknownFallback は複製で設定が保たれることを確かめる。
func TestObject_CloneKeepsUnknownFallback(t *testing.T) {
	o := NewObject()
	o.SetUnknownFallback("steps.x.outputs")

	v, _ := o.Clone().Get("y")
	if !v.IsUnknown() {
		t.Error("複製でも未知のキーは不明値になるべきです")
	}
}

// TestUnknown_JSONIsNull は不明値が JSON では null として出力されることを確かめる。
func TestUnknown_JSONIsNull(t *testing.T) {
	if got := Unknown("a.b").JSON(false); got != "null" {
		t.Errorf("JSON 表現: got %s, want null", got)
	}
}
