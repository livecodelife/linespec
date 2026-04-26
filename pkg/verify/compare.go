package verify

import "fmt"

// CompareJSON does a deep subset comparison of two JSON-decoded values: every key
// present in expected must exist in actual with a matching value. Extra keys in
// actual are ignored. Array elements are compared positionally and must match
// exactly (same length, same element values).
func CompareJSON(expected, actual interface{}) error {
	return compareJSONRecursive(expected, actual, "body")
}

func compareJSONRecursive(exp, act interface{}, path string) error {
	switch e := exp.(type) {
	case map[string]interface{}:
		a, ok := act.(map[string]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected object, got %T", path, act)
		}
		for k, v := range e {
			if err := compareJSONRecursive(v, a[k], path+"."+k); err != nil {
				return err
			}
		}
	case []interface{}:
		a, ok := act.([]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected array, got %T", path, act)
		}
		if len(e) != len(a) {
			return fmt.Errorf("at %s: expected array length %d, got %d", path, len(e), len(a))
		}
		for i := range e {
			if err := compareJSONRecursive(e[i], a[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	default:
		if fmt.Sprintf("%v", exp) != fmt.Sprintf("%v", act) {
			return fmt.Errorf("at %s: expected %v, got %v", path, exp, act)
		}
	}
	return nil
}
