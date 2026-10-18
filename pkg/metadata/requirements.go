// relationships:
//
//	references: [merge]
package metadata

import (
	"fmt"
	"regexp"
	"strconv"
)

var bytePattern = regexp.MustCompile(`^(\d+)([tgmk]b)?$`)

func bytes(value any) float64 {
	m := bytePattern.FindStringSubmatch(fmt.Sprint(value))
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	if m[2] != "" {
		n *= map[byte]float64{'t': 1 << 40, 'g': 1 << 30, 'm': 1 << 20, 'k': 1 << 10}[m[2][0]]
	}
	return n
}
func number(value any) float64 { n, _ := strconv.ParseFloat(fmt.Sprint(value), 64); return n }
func mergeHostRequirements(a, b map[string]any) map[string]any {
	cpus := max(number(a["cpus"]), number(b["cpus"]))
	memory := max(bytes(a["memory"]), bytes(b["memory"]))
	storage := max(bytes(a["storage"]), bytes(b["storage"]))
	gpu := mergeGPU(a["gpu"], b["gpu"])
	if cpus == 0 && memory == 0 && storage == 0 && (gpu == nil || gpu == false) {
		return nil
	}
	out := map[string]any{"cpus": cpus}
	if memory > 0 {
		out["memory"] = strconv.FormatFloat(memory, 'f', -1, 64)
	}
	if storage > 0 {
		out["storage"] = strconv.FormatFloat(storage, 'f', -1, 64)
	}
	if gpu != nil {
		out["gpu"] = gpu
	}
	return out
}
func mergeGPU(a, b any) any {
	if a == nil || a == false {
		return b
	}
	if b == nil || b == false {
		return a
	}
	if fmt.Sprint(a) == "optional" && fmt.Sprint(b) == "optional" {
		return "optional"
	}
	am, _ := a.(map[string]any)
	bm, _ := b.(map[string]any)
	cores := max(number(am["cores"]), number(bm["cores"]))
	memory := max(bytes(am["memory"]), bytes(bm["memory"]))
	out := map[string]any{}
	if cores > 0 {
		out["cores"] = cores
	}
	if memory > 0 {
		out["memory"] = strconv.FormatFloat(memory, 'f', -1, 64)
	}
	return out
}
