package agent

import (
	"errors"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/telemetry"
)

// Option customizes the Agent returned by New.
type Option func(*basicAgent) error

// WithTelemetry enables OpenTelemetry instrumentation for the agent and
// propagates the manager as the global default so other packages can emit
// consistent spans.
func WithTelemetry(mgr *telemetry.Manager) Option {
	return guardOption(func(a *basicAgent) error {
		if mgr == nil {
			return errors.New("telemetry manager is nil")
		}
		telemetry.SetDefault(mgr)
		a.telemetry = mgr
		return nil
	})
}

// WithModel injects the language model used for non-tool responses.
func WithModel(m model.Model) Option {
	return guardOption(func(a *basicAgent) error {
		if m == nil {
			return errors.New("model is nil")
		}
		a.model = m
		return nil
	})
}

// WithSession attaches a conversation session leveraged for LLM context.
func WithSession(sess session.Session) Option {
	return guardOption(func(a *basicAgent) error {
		if sess == nil {
			return errors.New("session is nil")
		}
		a.session = sess
		a.configureRecoveryComponents()
		return nil
	})
}

func guardOption(apply func(*basicAgent) error) Option {
	return func(a *basicAgent) error {
		if a == nil {
			return errors.New("agent is nil")
		}
		return apply(a)
	}
}
