// relationships:
//
//	references: [inventory]
package cli

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
)

func TestRegisteredOptionsIncludesInheritedHiddenAndNested(t *testing.T) {
	root := &cobra.Command{Use: "sample"}
	root.PersistentFlags().Bool("shared", false, "")
	group := &cobra.Command{Use: "group"}
	group.PersistentFlags().String("group-option", "", "")
	leaf := &cobra.Command{Use: "leaf <value>", Run: func(*cobra.Command, []string) { t.Fatal("inventory executed an action") }}
	leaf.Flags().StringP("hidden-option", "x", "", "")
	if err := leaf.Flags().MarkHidden("hidden-option"); err != nil {
		t.Fatal(err)
	}
	group.AddCommand(leaf)
	root.AddCommand(group)
	options := registeredOptions(root)
	for _, name := range []string{"shared", "group-option", "hidden-option", "help"} {
		if !slices.Contains(options["group leaf"], name) {
			t.Errorf("nested flag missing: %s in %+v", name, options)
		}
	}
	if slices.Contains(options["group leaf"], "x") || slices.Contains(options["group leaf"], "value") {
		t.Fatal("alias or positional counted as long flag")
	}
}
