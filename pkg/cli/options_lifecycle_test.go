// relationships:
//
//	references: [options_lifecycle, docs_test]
package cli

import (
	"testing"
)

func TestLifecycleOptionRegistration(t *testing.T) {
	root := (&application{}).command()
	for _, name := range []string{"up", "set-up", "run-user-commands"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		for flag, supported := range map[string]bool{
			"skip-non-blocking-commands": true,
			"prebuild":                   name != "set-up",
			"stop-for-personalization":   name == "run-user-commands",
			"expect-existing-container":  name == "up",
		} {
			f := cmd.Flags().Lookup(flag)
			if (f != nil) != supported {
				t.Fatalf("%s --%s registration=%v", name, flag, f)
			}
			if f != nil && (f.Value.Type() != "bool" || f.DefValue != "false" || f.Hidden) {
				t.Fatalf("%s --%s type/default/visibility=%+v", name, flag, f)
			}
		}
	}
}
