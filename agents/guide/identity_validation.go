package guide

import "strings"

func normalizeRoutingInfo(info *AgentRoutingInfo) (*AgentRoutingInfo, error) {
	if info == nil {
		return nil, nil
	}

	cloned := *info
	cloned.ID = strings.TrimSpace(info.ID)
	cloned.Type = strings.TrimSpace(info.Type)
	cloned.Name = strings.TrimSpace(info.Name)
	cloned.PodID = strings.TrimSpace(info.PodID)
	cloned.Aliases = normalizeAliases(info.Aliases)
	cloned.ActionShortcuts = normalizeActionShortcuts(info.ActionShortcuts)

	if cloned.ID == "" {
		return nil, ErrInvalidRoutingInfo("agent id is required")
	}
	if cloned.Type == "" {
		return nil, ErrInvalidRoutingInfo("agent type is required")
	}

	if info.Registration != nil {
		reg := *info.Registration
		reg.ID = strings.TrimSpace(reg.ID)
		reg.Name = strings.TrimSpace(reg.Name)
		reg.Aliases = normalizeAliases(reg.Aliases)
		if reg.ID == "" {
			reg.ID = cloned.ID
		}
		if reg.Name == "" {
			reg.Name = firstNonEmptyString(cloned.Name, cloned.Type)
		}
		if reg.ID != cloned.ID {
			return nil, ErrInvalidRoutingInfo("routing id and registration id must match")
		}
		cloned.Registration = &reg
		if cloned.Name == "" {
			cloned.Name = reg.Name
		}
	} else if cloned.Name == "" {
		cloned.Name = cloned.Type
	}

	return &cloned, nil
}

type invalidRoutingInfoError string

func (e invalidRoutingInfoError) Error() string {
	return string(e)
}

func ErrInvalidRoutingInfo(message string) error {
	return invalidRoutingInfoError(message)
}

func normalizeAliases(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeActionShortcuts(values []ActionShortcut) []ActionShortcut {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]ActionShortcut, 0, len(values))
	for _, shortcut := range values {
		shortcut.Name = strings.TrimSpace(shortcut.Name)
		shortcut.Description = strings.TrimSpace(shortcut.Description)
		if shortcut.Name == "" {
			continue
		}
		if _, ok := seen[shortcut.Name]; ok {
			continue
		}
		seen[shortcut.Name] = struct{}{}
		out = append(out, shortcut)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (g *Guide) normalizeChannelOwnedMessage(expectedAgentID string, msg *Message) *Message {
	expectedAgentID = strings.TrimSpace(expectedAgentID)
	if msg == nil || expectedAgentID == "" {
		return msg
	}

	displayName := strings.TrimSpace(g.resolveAgentDisplayName(expectedAgentID))
	changed := false
	cloned := *msg

	if strings.TrimSpace(cloned.SourceAgentID) != expectedAgentID {
		cloned.SourceAgentID = expectedAgentID
		changed = true
	}

	switch cloned.Type {
	case MessageTypeResponse:
		resp, ok := cloned.GetRouteResponse()
		if !ok || resp == nil {
			break
		}
		respCloned := *resp
		if strings.TrimSpace(respCloned.RespondingAgentID) != expectedAgentID {
			respCloned.RespondingAgentID = expectedAgentID
			changed = true
		}
		if strings.TrimSpace(respCloned.RespondingAgentName) == "" && displayName != "" {
			respCloned.RespondingAgentName = displayName
			changed = true
		}
		if changed {
			cloned.Payload = &respCloned
		}
	case MessageTypeStream:
		stream, ok := cloned.GetStreamResponse()
		if !ok || stream == nil {
			break
		}
		streamCloned := *stream
		if strings.TrimSpace(streamCloned.RespondingAgentID) != expectedAgentID {
			streamCloned.RespondingAgentID = expectedAgentID
			changed = true
		}
		if strings.TrimSpace(streamCloned.RespondingAgentName) == "" && displayName != "" {
			streamCloned.RespondingAgentName = displayName
			changed = true
		}
		if changed {
			streamCloned.Metadata = cloneMetadata(stream.Metadata)
			cloned.Payload = &streamCloned
		}
	}

	if !changed {
		return msg
	}
	return &cloned
}
