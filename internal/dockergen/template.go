package dockergen

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/template"
)

func getArrayValues(funcName string, entries interface{}) (*reflect.Value, error) {
	entriesVal := reflect.ValueOf(entries)

	kind := entriesVal.Kind()

	if kind == reflect.Ptr {
		entriesVal = reflect.Indirect(entriesVal)
		kind = entriesVal.Kind()
	}

	switch kind {
	case reflect.Array, reflect.Slice:
		break
	default:
		return nil, fmt.Errorf("must pass an array or slice to '%v'; received %v; kind %v", funcName, entries, kind)
	}
	return &entriesVal, nil
}

// Generalized groupBy function
func generalizedGroupBy(funcName string, entries interface{}, getValue func(interface{}) (interface{}, error), addEntry func(map[string][]interface{}, interface{}, interface{})) (map[string][]interface{}, error) {
	entriesVal, err := getArrayValues(funcName, entries)

	if err != nil {
		return nil, err
	}

	groups := make(map[string][]interface{})
	for i := 0; i < entriesVal.Len(); i++ {
		v := reflect.Indirect(entriesVal.Index(i)).Interface()
		value, err := getValue(v)
		if err != nil {
			return nil, err
		}
		if value != nil {
			addEntry(groups, value, v)
		}
	}
	return groups, nil
}

func generalizedGroupByKey(funcName string, entries interface{}, key string, addEntry func(map[string][]interface{}, interface{}, interface{})) (map[string][]interface{}, error) {
	getKey := func(v interface{}) (interface{}, error) {
		return deepGet(v, key), nil
	}
	return generalizedGroupBy(funcName, entries, getKey, addEntry)
}

// splitKeyValuePairs splits a input string into a map of key value pairs, first string is split by listSep into list items, then each list item is split by kvpSep into key value pair
// if a list item does not contai the kvpSep a defaultKey can be provided, where these values are grouped, or if omitted these values are used as key and value
func splitKeyValuePairs(input string, listSep string, kvpSep string, defaultKey ...string) map[string]string {
	keyValuePairs := strings.Split(input, listSep)

	output := map[string]string{}
	for _, kvp := range keyValuePairs {
		var key string
		var value string
		if strings.Contains(kvp, kvpSep) {
			splitted := strings.Split(kvp, kvpSep)
			key = splitted[0]
			value = splitted[1]
		} else if len(defaultKey) == 0 || defaultKey[0] == "" {
			// no key found, no default key specified
			key = kvp
			value = kvp
		} else {
			// no key found, use default key specified instead
			key = defaultKey[0]
			value = kvp
		}

		output[key] = value
	}

	return output
}

// groupByMultiKeyValuePairs similar to groupByMulti, but the key value ist split into a list (delimited by listSep) of key value pairs (seperated by kvpSep: <key>kvpSep<value, e.g key1=value1>)
// An array or slice entry will show up in the output map under all of the list key value pair keys
func groupByMultiKeyValuePairs(entries interface{}, key, listSep string, kvpSep string, defaultKey string) (map[string][]interface{}, error) {
	return generalizedGroupByKey("groupByMultiKeyValuePairs", entries, key, func(groups map[string][]interface{}, value interface{}, v interface{}) {

		keyValuePairs := splitKeyValuePairs(value.(string), listSep, kvpSep, defaultKey)
		for key := range keyValuePairs {
			groups[key] = append(groups[key], v)
		}
	})
}

// groupByMulti groups a generic array or slice by the path property keys value, where the path value is first split by sep into a list of key strings.
// An array or slice entry will show up in the output map under all of the list keys
func groupByMulti(entries interface{}, key, sep string) (map[string][]interface{}, error) {
	return generalizedGroupByKey("groupByMulti", entries, key, func(groups map[string][]interface{}, value interface{}, v interface{}) {
		items := strings.Split(value.(string), sep)
		for _, item := range items {
			groups[item] = append(groups[item], v)
		}
	})
}

// groupBy groups a generic array or slice by the path property key
func groupBy(entries interface{}, key string) (map[string][]interface{}, error) {
	return generalizedGroupByKey("groupBy", entries, key, func(groups map[string][]interface{}, value interface{}, v interface{}) {
		groups[value.(string)] = append(groups[value.(string)], v)
	})
}

// groupByKeys is the same as groupBy but only returns a list of keys
func groupByKeys(entries interface{}, key string) ([]string, error) {
	keys, err := generalizedGroupByKey("groupByKeys", entries, key, func(groups map[string][]interface{}, value interface{}, v interface{}) {
		groups[value.(string)] = append(groups[value.(string)], v)
	})

	if err != nil {
		return nil, err
	}

	ret := []string{}
	for k := range keys {
		ret = append(ret, k)
	}
	return ret, nil
}

// groupByLabel is the same as groupBy but over a given label
func groupByLabel(entries interface{}, label string) (map[string][]interface{}, error) {
	getLabel := func(v interface{}) (interface{}, error) {
		if container, ok := v.(RuntimeContainer); ok {
			if value, ok := container.Labels[label]; ok {
				return value, nil
			}
			return nil, nil
		}
		return nil, fmt.Errorf("must pass an array or slice of RuntimeContainer to 'groupByLabel'; received %v", v)
	}
	return generalizedGroupBy("groupByLabel", entries, getLabel, func(groups map[string][]interface{}, value interface{}, v interface{}) {
		groups[value.(string)] = append(groups[value.(string)], v)
	})
}

// Generalized where function
func generalizedWhere(funcName string, entries interface{}, key string, test func(interface{}) bool) (interface{}, error) {
	entriesVal, err := getArrayValues(funcName, entries)

	if err != nil {
		return nil, err
	}

	selection := make([]interface{}, 0)
	for i := 0; i < entriesVal.Len(); i++ {
		v := reflect.Indirect(entriesVal.Index(i)).Interface()

		value := deepGet(v, key)
		if test(value) {
			selection = append(selection, v)
		}
	}

	return selection, nil
}

// selects entries based on key
func where(entries interface{}, key string, cmp interface{}) (interface{}, error) {
	return generalizedWhere("where", entries, key, func(value interface{}) bool {
		return reflect.DeepEqual(value, cmp)
	})
}

// select entries where a key is not equal to a value
func whereNot(entries interface{}, key string, cmp interface{}) (interface{}, error) {
	return generalizedWhere("whereNot", entries, key, func(value interface{}) bool {
		return !reflect.DeepEqual(value, cmp)
	})
}

// selects entries where a key exists
func whereExist(entries interface{}, key string) (interface{}, error) {
	return generalizedWhere("whereExist", entries, key, func(value interface{}) bool {
		return value != nil
	})
}

// selects entries where a key does not exist
func whereNotExist(entries interface{}, key string) (interface{}, error) {
	return generalizedWhere("whereNotExist", entries, key, func(value interface{}) bool {
		return value == nil
	})
}

// selects entries based on key.  Assumes key is delimited and breaks it apart before comparing
func whereAny(entries interface{}, key, sep string, cmp []string) (interface{}, error) {
	return generalizedWhere("whereAny", entries, key, func(value interface{}) bool {
		if value == nil {
			return false
		} else {
			items := strings.Split(value.(string), sep)
			return len(intersect(cmp, items)) > 0
		}
	})
}

// selects entries based on key.  Assumes key is delimited and breaks it apart before comparing
func whereAll(entries interface{}, key, sep string, cmp []string) (interface{}, error) {
	req_count := len(cmp)
	return generalizedWhere("whereAll", entries, key, func(value interface{}) bool {
		if value == nil {
			return false
		} else {
			items := strings.Split(value.(string), sep)
			return len(intersect(cmp, items)) == req_count
		}
	})
}

// generalized whereLabel function
func generalizedWhereLabel(funcName string, containers Context, label string, test func(string, bool) bool) (Context, error) {
	selection := make([]*RuntimeContainer, 0)

	for i := 0; i < len(containers); i++ {
		container := containers[i]

		value, ok := container.Labels[label]
		if test(value, ok) {
			selection = append(selection, container)
		}
	}

	return selection, nil
}

// selects containers that have a particular label
func whereLabelExists(containers Context, label string) (Context, error) {
	return generalizedWhereLabel("whereLabelExists", containers, label, func(_ string, ok bool) bool {
		return ok
	})
}

// selects containers that have don't have a particular label
func whereLabelDoesNotExist(containers Context, label string) (Context, error) {
	return generalizedWhereLabel("whereLabelDoesNotExist", containers, label, func(_ string, ok bool) bool {
		return !ok
	})
}

// selects containers with a particular label whose value matches a regular expression
func whereLabelValueMatches(containers Context, label, pattern string) (Context, error) {
	rx, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	return generalizedWhereLabel("whereLabelValueMatches", containers, label, func(value string, ok bool) bool {
		return ok && rx.MatchString(value)
	})
}

// hasPrefix returns whether a given string is a prefix of another string
func hasPrefix(prefix, s string) bool {
	return strings.HasPrefix(s, prefix)
}

// hasSuffix returns whether a given string is a suffix of another string
func hasSuffix(suffix, s string) bool {
	return strings.HasSuffix(s, suffix)
}

func keys(input interface{}) (interface{}, error) {
	if input == nil {
		return nil, nil
	}

	val := reflect.ValueOf(input)
	if val.Kind() != reflect.Map {
		return nil, fmt.Errorf("cannot call keys on a non-map value: %v", input)
	}

	vk := val.MapKeys()
	k := make([]interface{}, val.Len())
	for i := range k {
		k[i] = vk[i].Interface()
	}

	return k, nil
}

func intersect(l1, l2 []string) []string {
	m := make(map[string]bool)
	m2 := make(map[string]bool)
	for _, v := range l2 {
		m2[v] = true
	}
	for _, v := range l1 {
		if m2[v] {
			m[v] = true
		}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func contains(input interface{}, key interface{}) bool {
	if input == nil {
		return false
	}

	val := reflect.ValueOf(input)
	if val.Kind() == reflect.Map {
		for _, k := range val.MapKeys() {
			if k.Interface() == key {
				return true
			}
		}
	}

	return false
}

func dict(values ...interface{}) (map[string]interface{}, error) {
	if len(values)%2 != 0 {
		return nil, errors.New("invalid dict call")
	}
	dict := make(map[string]interface{}, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			return nil, errors.New("dict keys must be strings")
		}
		dict[key] = values[i+1]
	}
	return dict, nil
}

func hashSha1(input string) string {
	h := sha1.New()
	io.WriteString(h, input)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func marshalJson(input interface{}) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if err := enc.Encode(input); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func unmarshalJson(input string) (interface{}, error) {
	var v interface{}
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		return nil, err
	}
	return v, nil
}

// arrayFirst returns first item in the array or nil if the
// input is nil or empty
func arrayFirst(input interface{}) interface{} {
	if input == nil {
		return nil
	}

	arr := reflect.ValueOf(input)

	if arr.Len() == 0 {
		return nil
	}

	return arr.Index(0).Interface()
}

// arrayLast returns last item in the array
func arrayLast(input interface{}) interface{} {
	arr := reflect.ValueOf(input)
	return arr.Index(arr.Len() - 1).Interface()
}

// arrayClosest find the longest matching substring in values
// that matches input
func arrayClosest(values []string, input string) string {
	best := ""
	for _, v := range values {
		if strings.Contains(input, v) && len(v) > len(best) {
			best = v
		}
	}
	return best
}

// dirList returns a list of files in the specified path
func dirList(path string) ([]string, error) {
	names := []string{}
	files, err := ioutil.ReadDir(path)
	if err != nil {
		log.Printf("Template error: %v", err)
		return names, nil
	}
	for _, f := range files {
		names = append(names, f.Name())
	}
	return names, nil
}

// coalesce returns the first non nil argument
func coalesce(input ...interface{}) interface{} {
	for _, v := range input {
		if v != nil {
			return v
		}
	}
	return nil
}

// trimPrefix returns a string without the prefix, if present
func trimPrefix(prefix, s string) string {
	return strings.TrimPrefix(s, prefix)
}

// trimSuffix returns a string without the suffix, if present
func trimSuffix(suffix, s string) string {
	return strings.TrimSuffix(s, suffix)
}

// trim returns the string without leading or trailing whitespace
func trim(s string) string {
	return strings.TrimSpace(s)
}

// toLower return the string in lower case
func toLower(s string) string {
	return strings.ToLower(s)
}

// toUpper return the string in upper case
func toUpper(s string) string {
	return strings.ToUpper(s)
}

// when returns the trueValue when the condition is true and the falseValue otherwise
func when(condition bool, trueValue, falseValue interface{}) interface{} {
	if condition {
		return trueValue
	} else {
		return falseValue
	}
}

func newTemplate(name string) *template.Template {
	tmpl := template.New(name).Funcs(template.FuncMap{
		"exists":                 pathExists,
		"toLower":                toLower,
		"toUpper":                toUpper,
		"closest":                   arrayClosest,
		"coalesce":                  coalesce,
		"contains":                  contains,
		"dict":                      dict,
		"dir":                       dirList,
		"first":                     arrayFirst,
		"groupBy":                   groupBy,
		"groupByKeys":               groupByKeys,
		"groupByMulti":              groupByMulti,
		"groupByMultiKeyValuePairs": groupByMultiKeyValuePairs,
		"groupByLabel":              groupByLabel,
		"hasPrefix":                 hasPrefix,
		"hasSuffix":                 hasSuffix,
		"json":                      marshalJson,
		"intersect":                 intersect,
		"keys":                      keys,
		"last":                      arrayLast,
		"replace":                   strings.Replace,
		"parseBool":                 strconv.ParseBool,
		"parseJson":                 unmarshalJson,
		"queryEscape":               url.QueryEscape,
		"sha1":                      hashSha1,
		"split":                     strings.Split,
		"splitN":                    strings.SplitN,
		"splitKeyValuePairs":        splitKeyValuePairs,
		"trimPrefix":                trimPrefix,
		"trimSuffix":                trimSuffix,
		"trim":                      trim,
		"when":                      when,
		"where":                     where,
		"whereNot":                  whereNot,
		"whereExist":                whereExist,
		"whereNotExist":             whereNotExist,
		"whereAny":                  whereAny,
		"whereAll":                  whereAll,
		"whereLabelExists":          whereLabelExists,
		"whereLabelDoesNotExist":    whereLabelDoesNotExist,
		"whereLabelValueMatches":    whereLabelValueMatches,
	})
	return tmpl
}

func filterRunning(config Config, containers Context) Context {
	if config.IncludeStopped {
		return containers
	} else {
		filteredContainers := Context{}
		for _, container := range containers {
			if container.State.Running {
				filteredContainers = append(filteredContainers, container)
			}
		}
		return filteredContainers
	}
}

func GenerateFile(config Config, containers Context) bool {
	filteredRunningContainers := filterRunning(config, containers)
	filteredContainers := Context{}
	if config.OnlyPublished {
		for _, container := range filteredRunningContainers {
			if len(container.PublishedAddresses()) > 0 {
				filteredContainers = append(filteredContainers, container)
			}
		}
	} else if config.OnlyExposed {
		for _, container := range filteredRunningContainers {
			if len(container.Addresses) > 0 {
				filteredContainers = append(filteredContainers, container)
			}
		}
	} else {
		filteredContainers = filteredRunningContainers
	}

	contents := executeTemplate(config.Template, filteredContainers)

	if !config.KeepBlankLines {
		buf := new(bytes.Buffer)
		removeBlankLines(bytes.NewReader(contents), buf)
		contents = buf.Bytes()
	}

	if config.Dest != "" {
		dest, err := ioutil.TempFile(filepath.Dir(config.Dest), "docker-gen")
		defer func() {
			dest.Close()
			os.Remove(dest.Name())
		}()
		if err != nil {
			log.Fatalf("Unable to create temp file: %s\n", err)
		}

		if n, err := dest.Write(contents); n != len(contents) || err != nil {
			log.Fatalf("Failed to write to temp file: wrote %d, exp %d, err=%v", n, len(contents), err)
		}

		oldContents := []byte{}
		if fi, err := os.Stat(config.Dest); err == nil || os.IsNotExist(err) {
			if err != nil && os.IsNotExist(err) {
				emptyFile, err := os.Create(config.Dest)
				if err != nil {
					log.Fatalf("Unable to create empty destination file: %s\n", err)
				} else {
					emptyFile.Close()
					fi, _ = os.Stat(config.Dest)
				}
			}
			if err := dest.Chmod(fi.Mode()); err != nil {
				log.Fatalf("Unable to chmod temp file: %s\n", err)
			}
			if err := dest.Chown(int(fi.Sys().(*syscall.Stat_t).Uid), int(fi.Sys().(*syscall.Stat_t).Gid)); err != nil {
				log.Fatalf("Unable to chown temp file: %s\n", err)
			}
			oldContents, err = ioutil.ReadFile(config.Dest)
			if err != nil {
				log.Fatalf("Unable to compare current file contents: %s: %s\n", config.Dest, err)
			}
		}

		if !bytes.Equal(oldContents, contents) {
			err = os.Rename(dest.Name(), config.Dest)
			if err != nil {
				log.Fatalf("Unable to create dest file %s: %s\n", config.Dest, err)
			}
			log.Printf("Generated '%s' from %d containers", config.Dest, len(filteredContainers))
			return true
		}
		return false
	} else {
		os.Stdout.Write(contents)
	}
	return true
}

func executeTemplate(templatePath string, containers Context) []byte {
	tmpl, err := newTemplate(filepath.Base(templatePath)).ParseFiles(templatePath)
	if err != nil {
		log.Fatalf("Unable to parse template: %s", err)
	}

	buf := new(bytes.Buffer)
	err = tmpl.ExecuteTemplate(buf, filepath.Base(templatePath), &containers)
	if err != nil {
		log.Fatalf("Template error: %s\n", err)
	}
	return buf.Bytes()
}
