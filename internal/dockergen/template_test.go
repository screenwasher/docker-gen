package dockergen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
)

type templateTestList []struct {
	tmpl     string
	context  interface{}
	expected string
}

func (tests templateTestList) run(t *testing.T, prefix string) {
	for n, test := range tests {
		tmplName := fmt.Sprintf("%s-test-%d", prefix, n)
		tmpl := template.Must(newTemplate(tmplName).Parse(test.tmpl))

		var b bytes.Buffer
		err := tmpl.ExecuteTemplate(&b, tmplName, test.context)
		if err != nil {
			t.Fatalf("Error executing template: %v (test %s)", err, tmplName)
		}

		got := b.String()
		if test.expected != got {
			t.Fatalf("Incorrect output found; expected %s, got %s (test %s)", test.expected, got, tmplName)
		}
	}
}

func TestGetArrayValues(t *testing.T) {
	values := []string{"foor", "bar", "baz"}
	var expectedType *reflect.Value

	arrayValues, err := getArrayValues("testFunc", values)
	assert.NoError(t, err)
	assert.IsType(t, expectedType, arrayValues)
	assert.Equal(t, "bar", arrayValues.Index(1).String())

	arrayValues, err = getArrayValues("testFunc", &values)
	assert.NoError(t, err)
	assert.IsType(t, expectedType, arrayValues)
	assert.Equal(t, "baz", arrayValues.Index(2).String())

	arrayValues, err = getArrayValues("testFunc", "foo")
	assert.Error(t, err)
	assert.Nil(t, arrayValues)
}

func TestContainsString(t *testing.T) {
	env := map[string]string{
		"PORT": "1234",
	}

	assert.True(t, contains(env, "PORT"))
	assert.False(t, contains(env, "MISSING"))
}

func TestContainsInteger(t *testing.T) {
	env := map[int]int{
		42: 1234,
	}

	assert.True(t, contains(env, 42))
	assert.False(t, contains(env, "WRONG TYPE"))
	assert.False(t, contains(env, 24))
}

func TestContainsNilInput(t *testing.T) {
	var env interface{} = nil

	assert.False(t, contains(env, 0))
	assert.False(t, contains(env, ""))
}

func TestKeys(t *testing.T) {
	env := map[string]string{
		"VIRTUAL_HOST": "demo.local",
	}
	tests := templateTestList{
		{`{{range (keys $)}}{{.}}{{end}}`, env, `VIRTUAL_HOST`},
	}

	tests.run(t, "keys")
}

func TestKeysEmpty(t *testing.T) {
	input := map[string]int{}

	k, err := keys(input)
	if err != nil {
		t.Fatalf("Error fetching keys: %v", err)
	}
	vk := reflect.ValueOf(k)
	if vk.Kind() == reflect.Invalid {
		t.Fatalf("Got invalid kind for keys: %v", vk)
	}

	if len(input) != vk.Len() {
		t.Fatalf("Incorrect key count; expected %d, got %d", len(input), vk.Len())
	}
}

func TestKeysNil(t *testing.T) {
	k, err := keys(nil)
	if err != nil {
		t.Fatalf("Error fetching keys: %v", err)
	}
	vk := reflect.ValueOf(k)
	if vk.Kind() != reflect.Invalid {
		t.Fatalf("Got invalid kind for keys: %v", vk)
	}
}

func TestIntersect(t *testing.T) {
	i := intersect([]string{"foo.fo.com", "bar.com"}, []string{"foo.bar.com"})
	assert.Len(t, i, 0, "Expected no match")

	i = intersect([]string{"foo.fo.com", "bar.com"}, []string{"bar.com", "foo.com"})
	assert.Len(t, i, 1, "Expected exactly one match")

	i = intersect([]string{"foo.com"}, []string{"bar.com", "foo.com"})
	assert.Len(t, i, 1, "Expected exactly one match")

	i = intersect([]string{"foo.fo.com", "foo.com", "bar.com"}, []string{"bar.com", "foo.com"})
	assert.Len(t, i, 2, "Expected exactly two matches")
}

func TestGroupByExistingKey(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "3",
		},
	}

	groups, err := groupBy(containers, "Env.VIRTUAL_HOST")

	assert.NoError(t, err)
	assert.Len(t, groups, 2)
	assert.Len(t, groups["demo1.localhost"], 2)
	assert.Len(t, groups["demo2.localhost"], 1)
	assert.Equal(t, "3", groups["demo2.localhost"][0].(RuntimeContainer).ID)
}

func TestGroupByAfterWhere(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
				"EXTERNAL":     "true",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
				"EXTERNAL":     "true",
			},
			ID: "3",
		},
	}

	filtered, _ := where(containers, "Env.EXTERNAL", "true")
	groups, err := groupBy(filtered, "Env.VIRTUAL_HOST")

	assert.NoError(t, err)
	assert.Len(t, groups, 2)
	assert.Len(t, groups["demo1.localhost"], 1)
	assert.Len(t, groups["demo2.localhost"], 1)
	assert.Equal(t, "3", groups["demo2.localhost"][0].(RuntimeContainer).ID)
}

func TestGroupByKeys(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "3",
		},
	}

	expected := []string{"demo1.localhost", "demo2.localhost"}
	groups, err := groupByKeys(containers, "Env.VIRTUAL_HOST")
	assert.NoError(t, err)
	assert.ElementsMatch(t, expected, groups)

	expected = []string{"1", "2", "3"}
	groups, err = groupByKeys(containers, "ID")
	assert.NoError(t, err)
	assert.ElementsMatch(t, expected, groups)
}

func TestGeneralizedGroupByError(t *testing.T) {
	groups, err := groupBy("string", "")
	assert.Error(t, err)
	assert.Nil(t, groups)
}

func TestGroupByLabel(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Labels: map[string]string{
				"com.docker.compose.project": "one",
			},
			ID: "1",
		},
		{
			Labels: map[string]string{
				"com.docker.compose.project": "two",
			},
			ID: "2",
		},
		{
			Labels: map[string]string{
				"com.docker.compose.project": "one",
			},
			ID: "3",
		},
		{
			ID: "4",
		},
		{
			Labels: map[string]string{
				"com.docker.compose.project": "",
			},
			ID: "5",
		},
	}

	groups, err := groupByLabel(containers, "com.docker.compose.project")

	assert.NoError(t, err)
	assert.Len(t, groups, 3)
	assert.Len(t, groups["one"], 2)
	assert.Len(t, groups[""], 1)
	assert.Len(t, groups["two"], 1)
	assert.Equal(t, "2", groups["two"][0].(RuntimeContainer).ID)
}

func TestGroupByLabelError(t *testing.T) {
	strings := []string{"foo", "bar", "baz"}
	groups, err := groupByLabel(strings, "")
	assert.Error(t, err)
	assert.Nil(t, groups)
}

func TestGroupByMulti(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost,demo3.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "3",
		},
	}

	groups, _ := groupByMulti(containers, "Env.VIRTUAL_HOST", ",")
	if len(groups) != 3 {
		t.Fatalf("expected 3 got %d", len(groups))
	}

	if len(groups["demo1.localhost"]) != 2 {
		t.Fatalf("expected 2 got %d", len(groups["demo1.localhost"]))
	}

	if len(groups["demo2.localhost"]) != 1 {
		t.Fatalf("expected 1 got %d", len(groups["demo2.localhost"]))
	}
	if groups["demo2.localhost"][0].(RuntimeContainer).ID != "3" {
		t.Fatalf("expected 2 got %s", groups["demo2.localhost"][0].(RuntimeContainer).ID)
	}
	if len(groups["demo3.localhost"]) != 1 {
		t.Fatalf("expect 1 got %d", len(groups["demo3.localhost"]))
	}
	if groups["demo3.localhost"][0].(RuntimeContainer).ID != "2" {
		t.Fatalf("expected 2 got %s", groups["demo3.localhost"][0].(RuntimeContainer).ID)
	}
}

func TestGroupByMultiKeyValuePairs(t *testing.T) {
	containers := []*RuntimeContainer{
		&RuntimeContainer{
			Env: map[string]string{
				"VIRTUAL_PORT": "443:3000,3000:3000",
			},
			ID: "1",
		},
		&RuntimeContainer{
			Env: map[string]string{
				"VIRTUAL_PORT": "1111,250:360",
			},
			ID: "2",
		},
		&RuntimeContainer{
			Env: map[string]string{
				"VIRTUAL_PORT": "123",
			},
			ID: "3",
		},
	}

	groups, _ := groupByMultiKeyValuePairs(containers, "Env.VIRTUAL_PORT", ",", ":", "445")
	if len(groups) != 4 {
		t.Fatalf("expected 4 got %d", len(groups))
	}

	if len(groups["445"]) != 2 {
		t.Fatalf("expected 2 got %d", len(groups["445"]))
	}

	if len(groups["443"]) != 1 {
		t.Fatalf("expected 1 got %d", len(groups["443"]))
	}
	if groups["445"][1].(RuntimeContainer).ID != "3" {
		t.Fatalf("expected 1 got %s", groups["445"][0].(RuntimeContainer).ID)
	}
	if len(groups["3000"]) != 1 {
		t.Fatalf("expect 1 got %d", len(groups["3000"]))
	}
	if groups["250"][0].(RuntimeContainer).ID != "2" {
		t.Fatalf("expected 1 got %s", groups["250"][0].(RuntimeContainer).ID)
	}
}

func TestSplitKeyValuePairs1(t *testing.T) {
	list := splitKeyValuePairs("key=value,1=2,test", ",", "=")

	if len(list) != 3 {
		t.Fatalf("expected 3 got %d", len(list))
	}
	if list["test"] != "test" {
		t.Fatalf("expected value 'test' got '%s'", list["test"])
	}
}

func TestSplitKeyValuePairs2(t *testing.T) {
	list := splitKeyValuePairs("key:value/1:2/test", "/", ":")

	if len(list) != 3 {
		t.Fatalf("expected 3 got %d", len(list))
	}
	if list["test"] != "test" {
		t.Fatalf("expected value 'test' got '%s'", list["test"])
	}
}

func TestWhere(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
			Addresses: []Address{
				{
					IP:    "172.16.42.1",
					Port:  "80",
					Proto: "tcp",
				},
			},
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "2",
			Addresses: []Address{
				{
					IP:    "172.16.42.1",
					Port:  "9999",
					Proto: "tcp",
				},
			},
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo3.localhost",
			},
			ID: "3",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "4",
		},
	}

	tests := templateTestList{
		{`{{where . "Env.VIRTUAL_HOST" "demo1.localhost" | len}}`, containers, `1`},
		{`{{where . "Env.VIRTUAL_HOST" "demo2.localhost" | len}}`, containers, `2`},
		{`{{where . "Env.VIRTUAL_HOST" "demo3.localhost" | len}}`, containers, `1`},
		{`{{where . "Env.NOEXIST" "demo3.localhost" | len}}`, containers, `0`},
		{`{{where .Addresses "Port" "80" | len}}`, containers[0], `1`},
		{`{{where .Addresses "Port" "80" | len}}`, containers[1], `0`},
		{
			`{{where . "Value" 5 | len}}`,
			[]struct {
				Value int
			}{
				{Value: 5},
				{Value: 3},
				{Value: 5},
			},
			`2`,
		},
	}

	tests.run(t, "where")
}

func TestWhereNot(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
			Addresses: []Address{
				{
					IP:    "172.16.42.1",
					Port:  "80",
					Proto: "tcp",
				},
			},
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "2",
			Addresses: []Address{
				{
					IP:    "172.16.42.1",
					Port:  "9999",
					Proto: "tcp",
				},
			},
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo3.localhost",
			},
			ID: "3",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "4",
		},
	}

	tests := templateTestList{
		{`{{whereNot . "Env.VIRTUAL_HOST" "demo1.localhost" | len}}`, containers, `3`},
		{`{{whereNot . "Env.VIRTUAL_HOST" "demo2.localhost" | len}}`, containers, `2`},
		{`{{whereNot . "Env.VIRTUAL_HOST" "demo3.localhost" | len}}`, containers, `3`},
		{`{{whereNot . "Env.NOEXIST" "demo3.localhost" | len}}`, containers, `4`},
		{`{{whereNot .Addresses "Port" "80" | len}}`, containers[0], `0`},
		{`{{whereNot .Addresses "Port" "80" | len}}`, containers[1], `1`},
		{
			`{{whereNot . "Value" 5 | len}}`,
			[]struct {
				Value int
			}{
				{Value: 5},
				{Value: 3},
				{Value: 5},
			},
			`1`,
		},
	}

	tests.run(t, "whereNot")
}

func TestWhereExist(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
				"VIRTUAL_PATH": "/api",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo3.localhost",
				"VIRTUAL_PATH": "/api",
			},
			ID: "3",
		},
		{
			Env: map[string]string{
				"VIRTUAL_PROTO": "https",
			},
			ID: "4",
		},
	}

	tests := templateTestList{
		{`{{whereExist . "Env.VIRTUAL_HOST" | len}}`, containers, `3`},
		{`{{whereExist . "Env.VIRTUAL_PATH" | len}}`, containers, `2`},
		{`{{whereExist . "Env.NOT_A_KEY" | len}}`, containers, `0`},
		{`{{whereExist . "Env.VIRTUAL_PROTO" | len}}`, containers, `1`},
	}

	tests.run(t, "whereExist")
}

func TestWhereNotExist(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
				"VIRTUAL_PATH": "/api",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo3.localhost",
				"VIRTUAL_PATH": "/api",
			},
			ID: "3",
		},
		{
			Env: map[string]string{
				"VIRTUAL_PROTO": "https",
			},
			ID: "4",
		},
	}

	tests := templateTestList{
		{`{{whereNotExist . "Env.VIRTUAL_HOST" | len}}`, containers, `1`},
		{`{{whereNotExist . "Env.VIRTUAL_PATH" | len}}`, containers, `2`},
		{`{{whereNotExist . "Env.NOT_A_KEY" | len}}`, containers, `4`},
		{`{{whereNotExist . "Env.VIRTUAL_PROTO" | len}}`, containers, `3`},
	}

	tests.run(t, "whereNotExist")
}

func TestWhereSomeMatch(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost,demo4.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "bar,demo3.localhost,foo",
			},
			ID: "3",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "4",
		},
	}

	tests := templateTestList{
		{`{{whereAny . "Env.VIRTUAL_HOST" "," (split "demo1.localhost" ",") | len}}`, containers, `1`},
		{`{{whereAny . "Env.VIRTUAL_HOST" "," (split "demo2.localhost,lala" ",") | len}}`, containers, `2`},
		{`{{whereAny . "Env.VIRTUAL_HOST" "," (split "something,demo3.localhost" ",") | len}}`, containers, `1`},
		{`{{whereAny . "Env.NOEXIST" "," (split "demo3.localhost" ",") | len}}`, containers, `0`},
	}

	tests.run(t, "whereAny")
}

func TestWhereRequires(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost,demo4.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "bar,demo3.localhost,foo",
			},
			ID: "3",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "4",
		},
	}

	tests := templateTestList{
		{`{{whereAll . "Env.VIRTUAL_HOST" "," (split "demo1.localhost" ",") | len}}`, containers, `1`},
		{`{{whereAll . "Env.VIRTUAL_HOST" "," (split "demo2.localhost,lala" ",") | len}}`, containers, `0`},
		{`{{whereAll . "Env.VIRTUAL_HOST" "," (split "demo3.localhost" ",") | len}}`, containers, `1`},
		{`{{whereAll . "Env.NOEXIST" "," (split "demo3.localhost" ",") | len}}`, containers, `0`},
	}

	tests.run(t, "whereAll")
}

func TestWhereLabelExists(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Labels: map[string]string{
				"com.example.foo": "foo",
				"com.example.bar": "bar",
			},
			ID: "1",
		},
		{
			Labels: map[string]string{
				"com.example.bar": "bar",
			},
			ID: "2",
		},
	}

	tests := templateTestList{
		{`{{whereLabelExists . "com.example.foo" | len}}`, containers, `1`},
		{`{{whereLabelExists . "com.example.bar" | len}}`, containers, `2`},
		{`{{whereLabelExists . "com.example.baz" | len}}`, containers, `0`},
	}

	tests.run(t, "whereLabelExists")
}

func TestWhereLabelDoesNotExist(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Labels: map[string]string{
				"com.example.foo": "foo",
				"com.example.bar": "bar",
			},
			ID: "1",
		},
		{
			Labels: map[string]string{
				"com.example.bar": "bar",
			},
			ID: "2",
		},
	}

	tests := templateTestList{
		{`{{whereLabelDoesNotExist . "com.example.foo" | len}}`, containers, `1`},
		{`{{whereLabelDoesNotExist . "com.example.bar" | len}}`, containers, `0`},
		{`{{whereLabelDoesNotExist . "com.example.baz" | len}}`, containers, `2`},
	}

	tests.run(t, "whereLabelDoesNotExist")
}

func TestWhereLabelValueMatches(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Labels: map[string]string{
				"com.example.foo": "foo",
				"com.example.bar": "bar",
			},
			ID: "1",
		},
		{
			Labels: map[string]string{
				"com.example.bar": "BAR",
			},
			ID: "2",
		},
	}

	tests := templateTestList{
		{`{{whereLabelValueMatches . "com.example.foo" "^foo$" | len}}`, containers, `1`},
		{`{{whereLabelValueMatches . "com.example.foo" "\\d+" | len}}`, containers, `0`},
		{`{{whereLabelValueMatches . "com.example.bar" "^bar$" | len}}`, containers, `1`},
		{`{{whereLabelValueMatches . "com.example.bar" "^(?i)bar$" | len}}`, containers, `2`},
		{`{{whereLabelValueMatches . "com.example.bar" ".*" | len}}`, containers, `2`},
		{`{{whereLabelValueMatches . "com.example.baz" ".*" | len}}`, containers, `0`},
	}

	tests.run(t, "whereLabelValueMatches")
}

func TestHasPrefix(t *testing.T) {
	const prefix = "tcp://"
	const str = "tcp://127.0.0.1:2375"
	if !hasPrefix(prefix, str) {
		t.Fatalf("expected %s to have prefix %s", str, prefix)
	}
}

func TestHasSuffix(t *testing.T) {
	const suffix = ".local"
	const str = "myhost.local"
	if !hasSuffix(suffix, str) {
		t.Fatalf("expected %s to have suffix %s", str, suffix)
	}
}

func TestSplitN(t *testing.T) {
	tests := templateTestList{
		{`{{index (splitN . "/" 2) 0}}`, "example.com/path", `example.com`},
		{`{{index (splitN . "/" 2) 1}}`, "example.com/path", `path`},
		{`{{index (splitN . "/" 2) 1}}`, "example.com/a/longer/path", `a/longer/path`},
		{`{{len (splitN . "/" 2)}}`, "example.com", `1`},
	}

	tests.run(t, "splitN")
}

func TestTrimPrefix(t *testing.T) {
	const prefix = "tcp://"
	const str = "tcp://127.0.0.1:2375"
	const trimmed = "127.0.0.1:2375"
	got := trimPrefix(prefix, str)
	if got != trimmed {
		t.Fatalf("expected trimPrefix(%s,%s) to be %s, got %s", prefix, str, trimmed, got)
	}
}

func TestTrimSuffix(t *testing.T) {
	const suffix = ".local"
	const str = "myhost.local"
	const trimmed = "myhost"
	got := trimSuffix(suffix, str)
	if got != trimmed {
		t.Fatalf("expected trimSuffix(%s,%s) to be %s, got %s", suffix, str, trimmed, got)
	}
}

func TestTrim(t *testing.T) {
	const str = "  myhost.local  "
	const trimmed = "myhost.local"
	got := trim(str)
	if got != trimmed {
		t.Fatalf("expected trim(%s) to be %s, got %s", str, trimmed, got)
	}
}

func TestToLower(t *testing.T) {
	const str = ".RaNd0m StrinG_"
	const lowered = ".rand0m string_"
	assert.Equal(t, lowered, toLower(str), "Unexpected value from toLower()")
}

func TestToUpper(t *testing.T) {
	const str = ".RaNd0m StrinG_"
	const uppered = ".RAND0M STRING_"
	assert.Equal(t, uppered, toUpper(str), "Unexpected value from toUpper()")
}

func TestDict(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost,demo3.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "3",
		},
	}
	d, err := dict("/", containers)
	if err != nil {
		t.Fatal(err)
	}
	if d["/"] == nil {
		t.Fatalf("did not find containers in dict: %s", d)
	}
	if d["MISSING"] != nil {
		t.Fail()
	}
}

func TestSha1(t *testing.T) {
	sum := hashSha1("/path")
	if sum != "4f26609ad3f5185faaa9edf1e93aa131e2131352" {
		t.Fatal("Incorrect SHA1 sum")
	}
}

func TestJson(t *testing.T) {
	containers := []*RuntimeContainer{
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost",
			},
			ID: "1",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo1.localhost,demo3.localhost",
			},
			ID: "2",
		},
		{
			Env: map[string]string{
				"VIRTUAL_HOST": "demo2.localhost",
			},
			ID: "3",
		},
	}
	output, err := marshalJson(containers)
	if err != nil {
		t.Fatal(err)
	}

	buf := bytes.NewBufferString(output)
	dec := json.NewDecoder(buf)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []*RuntimeContainer
	if err := dec.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(containers) {
		t.Fatalf("Incorrect unmarshaled container count. Expected %d, got %d.", len(containers), len(decoded))
	}
}

func TestParseJson(t *testing.T) {
	tests := templateTestList{
		{`{{parseJson .}}`, `null`, `<no value>`},
		{`{{parseJson .}}`, `true`, `true`},
		{`{{parseJson .}}`, `1`, `1`},
		{`{{parseJson .}}`, `0.5`, `0.5`},
		{`{{index (parseJson .) "enabled"}}`, `{"enabled":true}`, `true`},
		{`{{index (parseJson . | first) "enabled"}}`, `[{"enabled":true}]`, `true`},
	}

	tests.run(t, "parseJson")
}

func TestQueryEscape(t *testing.T) {
	tests := templateTestList{
		{`{{queryEscape .}}`, `example.com`, `example.com`},
		{`{{queryEscape .}}`, `.example.com`, `.example.com`},
		{`{{queryEscape .}}`, `*.example.com`, `%2A.example.com`},
		{`{{queryEscape .}}`, `~^example\.com(\..*\.xip\.io)?$`, `~%5Eexample%5C.com%28%5C..%2A%5C.xip%5C.io%29%3F%24`},
	}

	tests.run(t, "queryEscape")
}

func TestArrayClosestExact(t *testing.T) {
	if arrayClosest([]string{"foo.bar.com", "bar.com"}, "foo.bar.com") != "foo.bar.com" {
		t.Fatal("Expected foo.bar.com")
	}
}

func TestArrayClosestSubstring(t *testing.T) {
	if arrayClosest([]string{"foo.fo.com", "bar.com"}, "foo.bar.com") != "bar.com" {
		t.Fatal("Expected bar.com")
	}
}

func TestArrayClosestNoMatch(t *testing.T) {
	if arrayClosest([]string{"foo.fo.com", "bip.com"}, "foo.bar.com") != "" {
		t.Fatal("Expected ''")
	}
}

func TestWhen(t *testing.T) {
	context := struct {
		BoolValue   bool
		StringValue string
	}{
		true,
		"foo",
	}

	tests := templateTestList{
		{`{{ print (when .BoolValue "first" "second") }}`, context, `first`},
		{`{{ print (when (eq .StringValue "foo") "first" "second") }}`, context, `first`},

		{`{{ when (not .BoolValue) "first" "second" | print }}`, context, `second`},
		{`{{ when (not (eq .StringValue "foo")) "first" "second" | print }}`, context, `second`},
	}

	tests.run(t, "when")
}

func TestWhenTrue(t *testing.T) {
	if when(true, "first", "second") != "first" {
		t.Fatal("Expected first value")

	}
}

func TestWhenFalse(t *testing.T) {
	if when(false, "first", "second") != "second" {
		t.Fatal("Expected second value")
	}
}

func TestDirList(t *testing.T) {
	dir, err := ioutil.TempDir("", "dirList")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dir)

	files := map[string]string{
		"aaa": "",
		"bbb": "",
		"ccc": "",
	}
	// Create temporary files
	for key := range files {
		file, err := ioutil.TempFile(dir, key)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(file.Name())
		files[key] = file.Name()
	}

	expected := []string{
		path.Base(files["aaa"]),
		path.Base(files["bbb"]),
		path.Base(files["ccc"]),
	}

	filesList, _ := dirList(dir)
	assert.Equal(t, expected, filesList)

	filesList, _ = dirList("/wrong/path")
	assert.Equal(t, []string{}, filesList)
}

func TestCoalesce(t *testing.T) {
	v := coalesce(nil, "second", "third")
	assert.Equal(t, "second", v, "Expected second value")

	v = coalesce(nil, nil, nil)
	assert.Nil(t, v, "Expected nil value")
}
