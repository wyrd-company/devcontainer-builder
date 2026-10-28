// relationships:
//
// references: [documentation, documentation_jsonc]
package features

// DocumentationValue retains a publication value until a README uses it.
// A non-nil Properties map represents an object with its own properties; a
// non-nil Elements slice represents an array. Otherwise Text is a string.
// Objects inherit the ordinary object conversion methods. An own toString
// property shadows that method, including when its value is an empty string.
type DocumentationValue struct {
	Text       string
	Properties map[string]DocumentationValue
	Elements   []DocumentationValue
}

func documentationPublicationValue(value *DocumentationValue, fallback string) any {
	if value == nil {
		return fallback
	}
	if value.Properties != nil {
		object := &documentationObject{values: map[string]any{}, proto: documentationObjectPrototype{}}
		for key, property := range value.Properties {
			object.keys = append(object.keys, key)
			object.values[key] = documentationPublicationValue(&property, "")
		}
		return object
	}
	if value.Elements != nil {
		items := make([]any, len(value.Elements))
		for i := range value.Elements {
			items[i] = documentationPublicationValue(&value.Elements[i], "")
		}
		return items
	}
	return value.Text
}
