package analysis

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const incidentID = "bac4a3b9-d98f-46cc-a72d-72daba2b2715"

func fixture() Result {
	return Result{Summary: "Synthetic summary", PrimarySuspectedCause: "No diagnosis performed", ContributingFactors: []string{}, ObservedConditions: []string{"Synthetic input"}, RecommendedActions: []Action{}, SafetyNotes: []string{"Test only"}, Confidence: 0, Evidence: []Evidence{{SourceID: incidentID, Claim: "Synthetic incident"}}}
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func reject(t *testing.T, data []byte) {
	t.Helper()
	result, err := Decode(data, incidentID)
	if err != ErrInvalid || !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("expected sanitized error and zero result, got %v", err)
	}
}

func TestDecodeValid(t *testing.T) {
	for _, confidence := range []float64{0, 0.5, 1} {
		result := fixture()
		result.Confidence = confidence
		result.Summary = strings.Repeat("界", 2000)
		result.PrimarySuspectedCause = strings.Repeat("é", 1000)
		result.Evidence[0].SourceID = strings.ToUpper(incidentID)
		result.RecommendedActions = []Action{{1, "Synthetic action", "Test only"}, {2, "Second action", "Test only"}}
		got, err := Decode(encode(t, result), incidentID)
		if err != nil || !reflect.DeepEqual(got, result) {
			t.Fatalf("round trip: %v", err)
		}
	}
	for _, summary := range []string{`"\ud83d\ude80"`, `"\\ud800"`, `"�"`} {
		data := bytes.Replace(encode(t, fixture()), []byte(`"Synthetic summary"`), []byte(summary), 1)
		if _, err := Decode(data, incidentID); err != nil {
			t.Fatalf("valid Unicode %s: %v", summary, err)
		}
	}
}

func TestRequiredExactFields(t *testing.T) {
	r := fixture()
	r.RecommendedActions = []Action{{1, "Action", "Reason"}}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(encode(t, r), &root); err != nil {
		t.Fatal(err)
	}
	check := func(name string, fields map[string]json.RawMessage, wrap func(map[string]json.RawMessage) []byte) {
		for key, value := range fields {
			t.Run(name+"/"+key, func(t *testing.T) {
				delete(fields, key)
				reject(t, wrap(fields))
				fields[key] = json.RawMessage(`null`)
				reject(t, wrap(fields))
				delete(fields, key)
				fields[strings.ToUpper(key)] = value
				reject(t, wrap(fields))
				delete(fields, strings.ToUpper(key))
				fields[key] = value
			})
		}
		fields["source"] = json.RawMessage(`"fake"`)
		reject(t, wrap(fields))
		delete(fields, "source")
	}
	check("root", root, func(fields map[string]json.RawMessage) []byte { return encode(t, fields) })
	for _, name := range []string{"recommended_actions", "evidence"} {
		original := root[name]
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(original, &items); err != nil {
			t.Fatal(err)
		}
		check(name, items[0], func(fields map[string]json.RawMessage) []byte {
			root[name] = encode(t, []any{fields})
			return encode(t, root)
		})
		root[name] = original
	}
}

func TestDecodeMalformed(t *testing.T) {
	valid := string(encode(t, fixture()))
	cases := []string{"", "null", "[]", "true", "{}", valid + valid, valid + " secret-marker", valid[:len(valid)-1] + ",}", "/*comment*/" + valid,
		strings.Replace(valid, `"summary":`, `"summary":"secret-marker","summary":`, 1),
		strings.Replace(valid, `"summary":`, `"\u0073ummary":"secret-marker","summary":`, 1),
		strings.Replace(valid, `"source_id":`, `"source_id":"secret-marker","source_id":`, 1),
	}
	for _, replacement := range []string{`null`, `123`, `true`, `[]`, `{}`, `""`, `"  "`, `"\u0000"`, `"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, "\"\xff\""} {
		cases = append(cases, strings.Replace(valid, `"Synthetic summary"`, replacement, 1))
	}
	for _, replacement := range []string{`"0"`, `null`, `true`, `[]`, `-0.1`, `1.1`, `1e999`, `NaN`} {
		cases = append(cases, strings.Replace(valid, `"confidence":0`, `"confidence":`+replacement, 1))
	}
	for _, key := range []string{"contributing_factors", "observed_conditions", "recommended_actions", "safety_notes", "evidence"} {
		for _, value := range []string{`null`, `{}`, `"text"`, `[null]`, `[1]`} {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(valid), &fields); err != nil {
				t.Fatal(err)
			}
			fields[key] = json.RawMessage(value)
			cases = append(cases, string(encode(t, fields)))
		}
	}
	r := fixture()
	r.RecommendedActions = []Action{{1, "Action", "Reason"}}
	actionJSON := string(encode(t, r))
	for _, value := range []string{`0`, `-1`, `2`, `1.5`, `"1"`, `9999999999999999999999999999`} {
		cases = append(cases, strings.Replace(actionJSON, `"order":1`, `"order":`+value, 1))
	}
	cases = append(cases, strings.Replace(actionJSON, `"order":1`, `"order":1,"order":1`, 1))
	for i, data := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) { reject(t, []byte(data)) })
	}
}

func TestValidateBoundaries(t *testing.T) {
	cases := map[string]func(*Result){
		"long summary":             func(r *Result) { r.Summary = strings.Repeat("界", 2001) },
		"long cause":               func(r *Result) { r.PrimarySuspectedCause = strings.Repeat("a", 1001) },
		"blank cause":              func(r *Result) { r.PrimarySuspectedCause = "\t" },
		"invalid UTF8":             func(r *Result) { r.Summary = "\xff" },
		"NUL":                      func(r *Result) { r.Summary = "x\x00" },
		"nan":                      func(r *Result) { r.Confidence = math.NaN() },
		"infinity":                 func(r *Result) { r.Confidence = math.Inf(1) },
		"negative infinity":        func(r *Result) { r.Confidence = math.Inf(-1) },
		"low confidence":           func(r *Result) { r.Confidence = -0.1 },
		"high confidence":          func(r *Result) { r.Confidence = 1.1 },
		"nil actions":              func(r *Result) { r.RecommendedActions = nil },
		"nil evidence":             func(r *Result) { r.Evidence = nil },
		"empty evidence":           func(r *Result) { r.Evidence = []Evidence{} },
		"bad UUID":                 func(r *Result) { r.Evidence[0].SourceID = "bad" },
		"other UUID":               func(r *Result) { r.Evidence[0].SourceID = "aac4a3b9-d98f-46cc-a72d-72daba2b2715" },
		"blank claim":              func(r *Result) { r.Evidence[0].Claim = " " },
		"long claim":               func(r *Result) { r.Evidence[0].Claim = strings.Repeat("a", 1001) },
		"action gap":               func(r *Result) { r.RecommendedActions = []Action{{1, "a", "b"}, {3, "a", "b"}} },
		"action duplicate":         func(r *Result) { r.RecommendedActions = []Action{{1, "a", "b"}, {1, "a", "b"}} },
		"action blank description": func(r *Result) { r.RecommendedActions = []Action{{1, " ", "b"}} },
		"action blank reason":      func(r *Result) { r.RecommendedActions = []Action{{1, "a", " "}} },
		"action long description":  func(r *Result) { r.RecommendedActions = []Action{{1, strings.Repeat("a", 1001), "b"}} },
		"action long reason":       func(r *Result) { r.RecommendedActions = []Action{{1, "a", strings.Repeat("a", 1001)}} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := fixture()
			mutate(&r)
			if Validate(r, incidentID) != ErrInvalid {
				t.Fatal("accepted invalid result")
			}
		})
	}
	for _, id := range []string{"", "bad", strings.ReplaceAll(incidentID, "-", ""), "gac4a3b9-d98f-46cc-a72d-72daba2b2715"} {
		if Validate(fixture(), id) != ErrInvalid {
			t.Fatal("accepted invalid incident UUID")
		}
		if _, err := Decode(encode(t, fixture()), id); err != ErrInvalid {
			t.Fatal("decoded invalid incident UUID")
		}
	}
	for _, name := range []string{"factors", "observations", "safety"} {
		for _, values := range [][]string{nil, {" "}, {strings.Repeat("a", 1001)}, {"\xff"}, {"x\x00"}, make([]string, 11)} {
			r := fixture()
			switch name {
			case "factors":
				r.ContributingFactors = values
			case "observations":
				r.ObservedConditions = values
			case "safety":
				r.SafetyNotes = values
			}
			if Validate(r, incidentID) != ErrInvalid {
				t.Fatalf("accepted invalid %s", name)
			}
		}
	}
	r := fixture()
	r.ObservedConditions = []string{}
	if Validate(r, incidentID) != ErrInvalid {
		t.Fatal("accepted empty observations")
	}
	r = fixture()
	r.SafetyNotes = []string{}
	if Validate(r, incidentID) != ErrInvalid {
		t.Fatal("accepted empty safety notes")
	}
	for _, count := range []int{10, 11} {
		r = fixture()
		for i := 0; i < count; i++ {
			r.ContributingFactors = append(r.ContributingFactors, "a")
			r.RecommendedActions = append(r.RecommendedActions, Action{i + 1, "a", "b"})
		}
		r.ObservedConditions = append([]string{}, r.ContributingFactors...)
		r.SafetyNotes = append([]string{}, r.ContributingFactors...)
		r.Evidence = nil
		for i := 0; i < count; i++ {
			r.Evidence = append(r.Evidence, Evidence{incidentID, "a"})
		}
		if (Validate(r, incidentID) == nil) != (count == 10) {
			t.Fatalf("wrong count boundary %d", count)
		}
	}
}

func TestSizeAndDepth(t *testing.T) {
	data := encode(t, fixture())
	data = append(data, bytes.Repeat([]byte(" "), MaxJSONBytes-len(data))...)
	if _, err := Decode(data, incidentID); err != nil {
		t.Fatal(err)
	}
	reject(t, append(data, ' '))
	r := fixture()
	for i := 0; i < 10; i++ {
		r.ContributingFactors = append(r.ContributingFactors, strings.Repeat("&", 1000))
		r.RecommendedActions = append(r.RecommendedActions, Action{i + 1, strings.Repeat("&", 1000), "test"})
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(r); err != nil {
		t.Fatal(err)
	}
	if raw.Len() >= MaxJSONBytes {
		t.Fatal("fixture must fit before serialization")
	}
	reject(t, raw.Bytes())
	if Validate(r, incidentID) != ErrInvalid {
		t.Fatal("accepted oversized serialized output")
	}
	for _, depth := range []int{16, 17} {
		data := strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)
		decoder := json.NewDecoder(strings.NewReader(data))
		decoder.UseNumber()
		if checkValue(decoder, 0) != (depth == 16) {
			t.Fatalf("depth %d", depth)
		}
		reject(t, []byte(data))
	}
}

func TestIndependentArrayLimits(t *testing.T) {
	for _, field := range []string{"contributing_factors", "observed_conditions", "safety_notes", "recommended_actions", "evidence"} {
		t.Run(field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encode(t, fixture()), &fields); err != nil {
				t.Fatal(err)
			}
			var items []any
			for i := 0; i < 11; i++ {
				switch field {
				case "recommended_actions":
					items = append(items, Action{i + 1, "Test", "Test"})
				case "evidence":
					items = append(items, Evidence{incidentID, "Test"})
				default:
					items = append(items, "Test")
				}
			}
			fields[field] = encode(t, items)
			reject(t, encode(t, fields))
			fields[field] = encode(t, items[:10])
			if _, err := Decode(encode(t, fields), incidentID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	data, _ := json.Marshal(fixture())
	f.Add(data)
	f.Add([]byte(`{"summary":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Decode(data, incidentID)
		if err != nil {
			if err != ErrInvalid || !reflect.DeepEqual(r, Result{}) {
				t.Fatal("unsafe rejection")
			}
			return
		}
		if Validate(r, incidentID) != nil {
			t.Fatal("decoded invalid result")
		}
		if _, err := Decode(encode(t, r), incidentID); err != nil {
			t.Fatal("failed validated round trip")
		}
	})
}
