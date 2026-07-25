package expr

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// evalCall は関数呼び出しを評価する。
func evalCall(n *callNode, ctx *Context) (Value, error) {
	// 関数名の大文字小文字は区別しない（toJSON と tojson は同じ）。
	name := strings.ToLower(n.name)

	args := make([]Value, 0, len(n.args))
	for _, a := range n.args {
		v, err := evalNode(a, ctx)
		if err != nil {
			return Null(), err
		}
		args = append(args, v)
	}

	switch name {
	// ステータス関数。ドライランでは Context.Status が結果を決める。
	case "success":
		if err := checkArity(n.p, n.name, args, 0, 0); err != nil {
			return Null(), err
		}
		return Bool(ctx.Status == StatusSuccess), nil
	case "failure":
		if err := checkArity(n.p, n.name, args, 0, 0); err != nil {
			return Null(), err
		}
		return Bool(ctx.Status == StatusFailure), nil
	case "cancelled":
		if err := checkArity(n.p, n.name, args, 0, 0); err != nil {
			return Null(), err
		}
		return Bool(ctx.Status == StatusCancelled), nil
	case "always":
		if err := checkArity(n.p, n.name, args, 0, 0); err != nil {
			return Null(), err
		}
		return Bool(true), nil

	case "contains":
		if err := checkArity(n.p, n.name, args, 2, 2); err != nil {
			return Null(), err
		}
		return fnContains(args[0], args[1]), nil

	case "startswith":
		if err := checkArity(n.p, n.name, args, 2, 2); err != nil {
			return Null(), err
		}
		return Bool(hasPrefixFold(args[0].AsString(), args[1].AsString())), nil

	case "endswith":
		if err := checkArity(n.p, n.name, args, 2, 2); err != nil {
			return Null(), err
		}
		return Bool(hasSuffixFold(args[0].AsString(), args[1].AsString())), nil

	case "format":
		if err := checkArity(n.p, n.name, args, 1, -1); err != nil {
			return Null(), err
		}
		return fnFormat(n.p, args)

	case "join":
		if err := checkArity(n.p, n.name, args, 1, 2); err != nil {
			return Null(), err
		}
		return fnJoin(args), nil

	case "tojson":
		if err := checkArity(n.p, n.name, args, 1, 1); err != nil {
			return Null(), err
		}
		return String(args[0].JSON(true)), nil

	case "fromjson":
		if err := checkArity(n.p, n.name, args, 1, 1); err != nil {
			return Null(), err
		}
		return fnFromJSON(n.p, args[0])

	case "hashfiles":
		if err := checkArity(n.p, n.name, args, 1, -1); err != nil {
			return Null(), err
		}
		return fnHashFiles(n.p, ctx, args)
	}

	return Null(), posErrorf(n.p, "関数 %s() は存在しません", n.name)
}

// checkArity は引数の個数を検証する。max が -1 の場合は上限を設けない。
func checkArity(pos int, name string, args []Value, min, max int) error {
	got := len(args)
	if got >= min && (max < 0 || got <= max) {
		return nil
	}
	switch {
	case min == max:
		return posErrorf(pos, "%s() の引数は%d個ですが%d個指定されています", name, min, got)
	case max < 0:
		return posErrorf(pos, "%s() の引数は%d個以上必要ですが%d個しかありません", name, min, got)
	default:
		return posErrorf(pos, "%s() の引数は%d〜%d個ですが%d個指定されています", name, min, max, got)
	}
}

// fnContains は contains() を評価する。
// 第1引数が配列なら要素の等価比較、それ以外は文字列としての部分一致で判定する。
// 文字列の比較は大文字小文字を区別しない。
func fnContains(search, item Value) Value {
	if search.Kind() == KindArray {
		for _, v := range search.Items() {
			if Equal(v, item) {
				return Bool(true)
			}
		}
		return Bool(false)
	}
	return Bool(strings.Contains(
		strings.ToLower(search.AsString()),
		strings.ToLower(item.AsString()),
	))
}

// hasPrefixFold は大文字小文字を区別せずに接頭辞を判定する。
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// hasSuffixFold は大文字小文字を区別せずに接尾辞を判定する。
func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// fnFormat は format() を評価する。
// {0} を対応する引数で置換し、{{ と }} はそれぞれ { と } として出力する。
func fnFormat(pos int, args []Value) (Value, error) {
	tmpl := args[0].AsString()
	repl := args[1:]

	var sb strings.Builder
	for i := 0; i < len(tmpl); i++ {
		switch tmpl[i] {
		case '{':
			if i+1 < len(tmpl) && tmpl[i+1] == '{' {
				sb.WriteByte('{')
				i++
				continue
			}
			rel := strings.IndexByte(tmpl[i:], '}')
			if rel < 0 {
				return Null(), posErrorf(pos, "format() の書式に閉じられていない { があります")
			}
			idxText := tmpl[i+1 : i+rel]
			idx, err := strconv.Atoi(idxText)
			if err != nil {
				return Null(), posErrorf(pos, "format() の {%s} を引数の番号として解釈できません", idxText)
			}
			if idx < 0 || idx >= len(repl) {
				return Null(), posErrorf(pos, "format() の {%d} に対応する引数がありません（引数は%d個）", idx, len(repl))
			}
			sb.WriteString(repl[idx].AsString())
			i += rel

		case '}':
			if i+1 < len(tmpl) && tmpl[i+1] == '}' {
				sb.WriteByte('}')
				i++
				continue
			}
			return Null(), posErrorf(pos, "format() の書式に対応する { のない } があります")

		default:
			sb.WriteByte(tmpl[i])
		}
	}
	return String(sb.String()), nil
}

// fnJoin は join() を評価する。区切り文字の既定値はカンマ。
func fnJoin(args []Value) Value {
	sep := ","
	if len(args) > 1 {
		sep = args[1].AsString()
	}
	v := args[0]
	if v.Kind() != KindArray {
		// 配列以外は単一の値として文字列化する。
		return String(v.AsString())
	}
	parts := make([]string, 0, len(v.Items()))
	for _, item := range v.Items() {
		parts = append(parts, item.AsString())
	}
	return String(strings.Join(parts, sep))
}

// fnFromJSON は fromJSON() を評価する。
func fnFromJSON(pos int, v Value) (Value, error) {
	s := strings.TrimSpace(v.AsString())
	if s == "" {
		return Null(), nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	// 整数と小数を区別せず float64 として扱うため json.Number を経由する。
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return Null(), posErrorf(pos, "fromJSON() の引数を JSON として解釈できません: %v", err)
	}
	out, err := decodeJSONValue(dec, tok)
	if err != nil {
		return Null(), posErrorf(pos, "fromJSON() の引数を JSON として解釈できません: %v", err)
	}
	return out, nil
}

// decodeJSONValue はトークン列から Value を組み立てる。
// encoding/json のマップ復元ではキーの順序が失われるため、
// toJSON の出力順や matrix の軸順を保つために自前で組み立てる。
func decodeJSONValue(dec *json.Decoder, tok json.Token) (Value, error) {
	switch t := tok.(type) {
	case nil:
		return Null(), nil
	case bool:
		return Bool(t), nil
	case string:
		return String(t), nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return Null(), fmt.Errorf("数値 %s を解釈できません", t.String())
		}
		return Number(f), nil
	case json.Delim:
		switch t {
		case '[':
			items := []Value{}
			for dec.More() {
				next, err := dec.Token()
				if err != nil {
					return Null(), err
				}
				item, err := decodeJSONValue(dec, next)
				if err != nil {
					return Null(), err
				}
				items = append(items, item)
			}
			// 閉じ括弧を読み捨てる。
			if _, err := dec.Token(); err != nil {
				return Null(), err
			}
			return Array(items), nil

		case '{':
			o := NewObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return Null(), err
				}
				key, ok := keyTok.(string)
				if !ok {
					return Null(), errors.New("オブジェクトのキーが文字列ではありません")
				}
				valTok, err := dec.Token()
				if err != nil {
					return Null(), err
				}
				val, err := decodeJSONValue(dec, valTok)
				if err != nil {
					return Null(), err
				}
				o.Set(key, val)
			}
			if _, err := dec.Token(); err != nil {
				return Null(), err
			}
			return Obj(o), nil
		}
	}
	return Null(), fmt.Errorf("解釈できないトークン %v があります", tok)
}

// fnHashFiles は hashFiles() を評価する。
// 実ファイルの読み取りが必要なため、Context に実装が無い場合はエラーにする。
func fnHashFiles(pos int, ctx *Context, args []Value) (Value, error) {
	if ctx.HashFiles == nil {
		return Null(), posErrorf(pos, "hashFiles() はこの評価環境では使えません")
	}
	patterns := make([]string, 0, len(args))
	for _, a := range args {
		patterns = append(patterns, a.AsString())
	}
	sum, err := ctx.HashFiles(patterns)
	if err != nil {
		return Null(), posErrorf(pos, "hashFiles() の評価に失敗しました: %v", err)
	}
	return String(sum), nil
}
