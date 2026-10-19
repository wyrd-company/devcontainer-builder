// relationships:
//
//	references: [options_probe]
package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestUserEnvProbeOptions(t *testing.T) {
	for _, name := range []string{"up", "set-up", "run-user-commands", "exec"} {
		t.Run(name, func(t *testing.T) {
			root := (&application{}).command()
			command, _, err := root.Find([]string{name})
			if err != nil {
				t.Fatal(err)
			}
			f := command.Flags().Lookup("default-user-env-probe")
			if f == nil || f.DefValue != "loginInteractiveShell" {
				t.Fatalf("flag = %+v", f)
			}
			argv := []string{name, "--default-user-env-probe=invalid"}
			if name == "set-up" {
				argv = append(argv, "--container-id", "sample")
			}
			if name == "exec" {
				argv = append(argv, "--", "true")
			}
			result, _ := invoke(t, 2, argv...)
			if result["message"] != "--default-user-env-probe must be none, loginInteractiveShell, interactiveShell or loginShell" {
				t.Fatalf("validation: %v", result)
			}
			for _, mode := range []string{"none", "loginInteractiveShell", "interactiveShell", "loginShell", "", "invalid"} {
				var o userEnvProbeOptions
				cmd := &cobra.Command{Use: "sample"}
				o.flags(cmd)
				if err := cmd.ParseFlags([]string{"--default-user-env-probe=" + mode}); err != nil {
					t.Fatal(err)
				}
				if valid := o.validate() == nil; valid != (mode != "" && mode != "invalid") {
					t.Fatalf("mode=%q valid=%v", mode, valid)
				}
			}
		})
	}
}
