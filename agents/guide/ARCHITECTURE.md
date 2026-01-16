# Guide Agent Architecture

## Overview

The Guide is the **central routing hub** for all inter-agent communication. Every request and response flows through the Guide via an **event bus**, which:

1. Routes requests from source agents to target agents
2. Receives responses from target agents
3. Returns responses to the correct source agent

**The Guide is a message broker.** It maintains correlation context to match responses with their original requests.

**The Guide does NOT execute queries. It routes messages between agents.**

---

## Event Bus Architecture

All inter-agent communication flows through an event bus using a channel-per-agent model.

### Agent Channels

Each agent registers three channels with the Guide:

| Channel | Pattern | Purpose |
|---------|---------|---------|
| Requests | `<agent>.requests` | Incoming work for the agent |
| Responses | `<agent>.responses` | Successful responses from the agent |
| Errors | `<agent>.errors` | Error responses from the agent |

Example for the Archivalist:
- `archivalist.requests` - Where Guide publishes forwarded requests
- `archivalist.responses` - Where Archivalist publishes responses
- `archivalist.errors` - Where Archivalist publishes errors

### Guide's Channels

The Guide itself has channels:
- `guide.requests` - Where agents publish requests for routing
- `agents.registry` - Where registration announcements are published

### Subscription Model

| Subscriber | Subscribes To | Purpose |
|------------|---------------|---------|
| Guide | `guide.requests` | Receive routing requests |
| Guide | `<agent>.responses` | Capture responses for correlation |
| Guide | `<agent>.errors` | Capture errors for correlation |
| Agent | `<self>.requests` | Receive work |
| Agent | `<self>.responses` | Receive replies to own requests |
| Agent | `agents.registry` | Learn about other agents |

---

## Message Flow

```
┌─────────────┐                    ┌─────────────┐                    ┌─────────────┐
│ Orchestrator│                    │    GUIDE    │                    │ Archivalist │
└──────┬──────┘                    └──────┬──────┘                    └──────┬──────┘
       │                                  │                                  │
       │  1. Publish to                   │                                  │
       │     guide.requests               │                                  │
       │ ────────────────────────────────>│                                  │
       │                                  │                                  │
       │                                  │  2. Classify, generate corr_id   │
       │                                  │     Store in pending             │
       │                                  │                                  │
       │                                  │  3. Publish to                   │
       │                                  │     archivalist.requests         │
       │                                  │ ─────────────────────────────────>│
       │                                  │                                  │
       │                                  │                                  │ 4. Process
       │                                  │                                  │
       │                                  │  5. Publish to                   │
       │                                  │     archivalist.responses        │
       │                                  │ <─────────────────────────────────│
       │                                  │                                  │
       │                                  │  6. Correlate, lookup source     │
       │                                  │                                  │
       │  7. Publish to                   │                                  │
       │     orchestrator.responses       │                                  │
       │ <────────────────────────────────│                                  │
       │                                  │                                  │
```

---

## Agent Registration

When an agent registers with the Guide:

1. **Agent provides**: `AgentRoutingInfo` (ID, capabilities, shortcuts, triggers)
2. **Guide creates**: Agent channels (`<id>.requests`, `<id>.responses`, `<id>.errors`)
3. **Guide subscribes to**: `<id>.responses` and `<id>.errors`
4. **Guide publishes**: Registration announcement to `agents.registry`
5. **Other agents receive**: Announcement with new agent's capabilities and channels

```go
// Agent registration
info := &guide.AgentRoutingInfo{
    ID:      "archivalist",
    Name:    "archivalist",
    Aliases: []string{"arch"},
    Capabilities: guide.AgentCapabilities{
        Intents: []guide.Intent{guide.IntentRecall, guide.IntentStore},
        Domains: []guide.Domain{guide.DomainPatterns, guide.DomainFailures},
    },
}

err := guide.Register(info)
```

### Registration Announcement

When an agent registers, all subscribers to `agents.registry` receive:

```go
type AgentAnnouncement struct {
    AgentID     string          `json:"agent_id"`
    AgentName   string          `json:"agent_name"`
    Aliases     []string        `json:"aliases,omitempty"`
    Channels    *AgentChannels  `json:"channels,omitempty"`
    Capabilities *AgentCapabilities `json:"capabilities,omitempty"`
    Description string          `json:"description,omitempty"`
}
```

---

## Message Types

### Request Message

Sent by a source agent to `guide.requests`:

```go
type RouteRequest struct {
    CorrelationID       string    `json:"correlation_id,omitempty"`
    ParentCorrelationID string    `json:"parent_correlation_id,omitempty"`
    Input               string    `json:"input"`
    SourceAgentID       string    `json:"source_agent_id"`
    SourceAgentName     string    `json:"source_agent_name,omitempty"`
    TargetAgentID       string    `json:"target_agent_id,omitempty"`
    FireAndForget       bool      `json:"fire_and_forget,omitempty"`
    SessionID           string    `json:"session_id,omitempty"`
    Timestamp           time.Time `json:"timestamp"`
}
```

### Forwarded Request

What the Guide publishes to `<target>.requests`:

```go
type ForwardedRequest struct {
    CorrelationID        string             `json:"correlation_id"`
    ParentCorrelationID  string             `json:"parent_correlation_id,omitempty"`
    Input                string             `json:"input"`
    Intent               Intent             `json:"intent"`
    Domain               Domain             `json:"domain"`
    Entities             *ExtractedEntities `json:"entities,omitempty"`
    SourceAgentID        string             `json:"source_agent_id"`
    SourceAgentName      string             `json:"source_agent_name,omitempty"`
    FireAndForget        bool               `json:"fire_and_forget,omitempty"`
    Confidence           float64            `json:"confidence"`
    ClassificationMethod string             `json:"classification_method"`
}
```

### Response Message

What agents publish to `<self>.responses`:

```go
type RouteResponse struct {
    CorrelationID       string        `json:"correlation_id"`
    Success             bool          `json:"success"`
    Data                any           `json:"data,omitempty"`
    Error               string        `json:"error,omitempty"`
    RespondingAgentID   string        `json:"responding_agent_id"`
    RespondingAgentName string        `json:"responding_agent_name,omitempty"`
    ProcessingTime      time.Duration `json:"processing_time,omitempty"`
}
```

---

## Fire-and-Forget Requests

For async sub-requests that don't need a response routed back:

```go
req := &RouteRequest{
    Input:               "@arch:store:failures{...}",
    SourceAgentID:       "librarian",
    FireAndForget:       true,
    ParentCorrelationID: originalCorrelationID,
}
```

When `FireAndForget: true`:
- Guide still routes to target
- Target processes but doesn't publish response
- Source continues without waiting

---

## Correlation Tracking

The Guide maintains pending requests for correlation:

```go
type PendingRequest struct {
    CorrelationID   string
    SourceAgentID   string
    TargetAgentID   string
    Request         *RouteRequest
    Classification  *RouteResult
    CreatedAt       time.Time
    ExpiresAt       time.Time
}
```

### Lifecycle

1. **Request arrives** at `guide.requests`
2. **Generate correlation ID**, store in pending
3. **Forward to target** via `<target>.requests`
4. **Response arrives** at `<target>.responses`
5. **Lookup by correlation ID**, find source
6. **Forward to source** via `<source>.responses`
7. **Remove from pending**

---

## DSL Syntax

### Request Routing

```
@to:<agent> <query>           # Direct route to agent
@guide <query>                # Intent-based routing (LLM classification)
@<agent>:<intent>:<domain>    # Full DSL with explicit routing
@<action> <query>             # Agent-registered action shortcut
```

### Token Efficiency

| Path | Cost | When Used |
|------|------|-----------|
| DSL command | 0 tokens | Agent-to-agent communication |
| Exact cache hit | 0 tokens | Repeat queries |
| LLM classification | ~250 tokens | Novel natural language |

---

## Classification

For natural language requests, the Guide classifies:

| Field | Purpose |
|-------|---------|
| Intent | What to do (recall, store, check, etc.) |
| Domain | Category (patterns, failures, files, etc.) |
| TemporalFocus | Past, present, or future |
| Entities | Extracted parameters |
| Confidence | 0.0 - 1.0 |

### Confidence Thresholds

| Score | Action |
|-------|--------|
| >= 0.90 | Route immediately |
| >= 0.75 | Route and log for review |
| >= 0.50 | Suggest, may request confirmation |
| < 0.50 | Reject with explanation |

---

## Public API

```go
// Create event bus
bus := guide.NewChannelBus(guide.DefaultChannelBusConfig())

// Create Guide with bus
g, err := guide.New(client, guide.Config{
    Bus: bus,
})

// Start listening
err = g.Start()

// Register an agent (creates channels, subscribes)
err = g.Register(agentRoutingInfo)

// Publish a request (for agents)
err = g.PublishRequest(request)

// Get agent channels
channels := g.GetAgentChannels("archivalist")
// channels.Requests  = "archivalist.requests"
// channels.Responses = "archivalist.responses"
// channels.Errors    = "archivalist.errors"

// Subscribe to registry announcements
sub, err := g.SubscribeToRegistry(handler)

// Stop and cleanup
err = g.Stop()
```

---

## Agent Implementation Pattern

```go
type MyAgent struct {
    id       string
    bus      guide.EventBus
    channels *guide.AgentChannels
    reqSub   guide.Subscription
    respSub  guide.Subscription
}

func (a *MyAgent) Start(bus guide.EventBus) error {
    a.bus = bus
    a.channels = guide.NewAgentChannels(a.id)

    // Subscribe to own request channel
    var err error
    a.reqSub, err = bus.SubscribeAsync(a.channels.Requests, a.handleRequest)
    if err != nil {
        return err
    }

    // Subscribe to own response channel (for replies to our requests)
    a.respSub, err = bus.SubscribeAsync(a.channels.Responses, a.handleResponse)
    return err
}

func (a *MyAgent) handleRequest(msg *guide.Message) error {
    fwd, ok := msg.GetForwardedRequest()
    if !ok {
        return nil
    }

    // Process the request...
    result, err := a.process(fwd)

    // Don't respond if fire-and-forget
    if fwd.FireAndForget {
        return nil
    }

    // Publish response to own response channel
    resp := &guide.RouteResponse{
        CorrelationID:     fwd.CorrelationID,
        Success:           err == nil,
        Data:              result,
        RespondingAgentID: a.id,
    }
    if err != nil {
        resp.Error = err.Error()
    }

    respMsg := guide.NewResponseMessage(generateID(), resp)
    return a.bus.Publish(a.channels.Responses, respMsg)
}
```

---

## Error Handling

| Error | Handling |
|-------|----------|
| Unknown target agent | Publish error to `<source>.responses` |
| Target rejects request | Publish error to `<source>.responses` |
| Response timeout | Expire pending, optionally notify source |
| Invalid correlation ID | Log warning, drop message |

---

## Configuration

```go
type Config struct {
    Bus            EventBus      // Required: event bus for messaging
    RouterConfig   RouterConfig  // Classification settings
    PendingTimeout time.Duration // Default: 5 minutes
    MaxPendingPerAgent int       // Default: 1000
    SessionID      string
    AgentID        string
    Registry       AgentRegistry // Optional: custom registry
}

type ChannelBusConfig struct {
    BufferSize int // Default: 256
}
```
