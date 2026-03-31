package skills

import "fmt"

func (s *Skill) Clone() *Skill {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Loaded = false
	clone.InputSchema = cloneInputSchema(s.InputSchema)
	if len(s.Keywords) > 0 {
		clone.Keywords = append([]string(nil), s.Keywords...)
	}
	if len(s.Examples) > 0 {
		clone.Examples = append([]string(nil), s.Examples...)
	}
	if len(s.BestPractices) > 0 {
		clone.BestPractices = append([]string(nil), s.BestPractices...)
	}
	if len(s.Requirements) > 0 {
		clone.Requirements = append([]string(nil), s.Requirements...)
	}
	if len(s.Satisfies) > 0 {
		clone.Satisfies = append([]string(nil), s.Satisfies...)
	}
	if len(s.Avoids) > 0 {
		clone.Avoids = append([]string(nil), s.Avoids...)
	}
	if s.ProviderTool != nil {
		providerTool := s.ProviderTool.Clone()
		clone.ProviderTool = &providerTool
	}
	return &clone
}

func CloneRegistry(base *Registry) (*Registry, error) {
	if base == nil {
		return nil, fmt.Errorf("skill registry is required")
	}
	clone := NewRegistry()
	for _, skill := range base.GetAll() {
		if skill == nil {
			continue
		}
		if err := clone.Register(skill.Clone()); err != nil {
			return nil, err
		}
	}
	return clone, nil
}

func cloneInputSchema(schema *InputSchema) *InputSchema {
	if schema == nil {
		return nil
	}
	clone := &InputSchema{
		Type:     schema.Type,
		Required: append([]string(nil), schema.Required...),
	}
	if len(schema.Properties) > 0 {
		clone.Properties = make(map[string]*Property, len(schema.Properties))
		for name, property := range schema.Properties {
			clone.Properties[name] = cloneProperty(property)
		}
	} else {
		clone.Properties = make(map[string]*Property)
	}
	return clone
}

func cloneProperty(property *Property) *Property {
	if property == nil {
		return nil
	}
	clone := &Property{
		Type:        property.Type,
		Description: property.Description,
		Default:     property.Default,
		Required:    append([]string(nil), property.Required...),
		Enum:        append([]string(nil), property.Enum...),
	}
	if property.Items != nil {
		clone.Items = cloneProperty(property.Items)
	}
	if len(property.Properties) > 0 {
		clone.Properties = make(map[string]*Property, len(property.Properties))
		for name, nested := range property.Properties {
			clone.Properties[name] = cloneProperty(nested)
		}
	}
	return clone
}
