// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package state

import (
	"time"

	"github.com/juju/errors"
	"github.com/juju/names"
	"gopkg.in/tomb.v1"

	coreagent "github.com/juju/juju/agent"
	"github.com/juju/juju/state"
	"github.com/juju/juju/worker"
	"github.com/juju/juju/worker/dependency"
)

// ManifoldConfig provides the dependencies for Manifold.
type ManifoldConfig struct {
	AgentName              string
	AgentConfigUpdatedName string
	OpenState              func(coreagent.Config) (*state.State, error)
	PingInterval           time.Duration
}

const defaultPingInterval = 15 * time.Second

// Manifold returns a manifold whose worker which wraps a *state.State
// and will exit if its associated mongodb session dies.
func Manifold(config ManifoldConfig) dependency.Manifold {
	return dependency.Manifold{
		Inputs: []string{
			config.AgentName,
			config.AgentConfigUpdatedName,
		},
		Start: func(getResource dependency.GetResourceFunc) (worker.Worker, error) {
			// First, a sanity check.
			if config.OpenState == nil {
				return nil, errors.New("OpenState is nil in config")
			}

			// Get the agent.
			var agent coreagent.Agent
			if err := getResource(config.AgentName, &agent); err != nil {
				return nil, err
			}

			agentConfig := agent.CurrentConfig()

			if _, ok := agentConfig.Tag().(names.MachineTag); !ok {
				return nil, errors.New("manifold may only be used in a machine agent")
			}

			// Can't continue if there's no StateServingInfo available.
			_, ok := agentConfig.StateServingInfo()
			if !ok {
				return nil, dependency.ErrMissing
			}

			st, err := config.OpenState(agentConfig)
			if err != nil {
				return nil, errors.Trace(err)
			}

			pingInterval := config.PingInterval
			if pingInterval == 0 {
				pingInterval = defaultPingInterval
			}

			w := &stateWorker{
				st:           st,
				pingInterval: pingInterval,
			}
			go func() {
				defer w.tomb.Done()
				w.tomb.Kill(w.loop())
			}()
			return w, nil
		},
		Output: outputFunc,
	}
}

// outputFunc extracts a *state.State from a *stateWorker.
func outputFunc(in worker.Worker, out interface{}) error {
	inWorker, _ := in.(*stateWorker)
	if inWorker == nil {
		return errors.Errorf("in should be a %T; got %T", inWorker, in)
	}

	switch outPointer := out.(type) {
	case **state.State:
		*outPointer = inWorker.st
	default:
		return errors.Errorf("out should be *state.State; got %T", out)
	}
	return nil
}

type stateWorker struct {
	tomb         tomb.Tomb
	st           *state.State
	pingInterval time.Duration
}

func (w *stateWorker) loop() error {
	for {
		select {
		case <-w.tomb.Dying():
			return tomb.ErrDying
		case <-time.After(w.pingInterval):
			err := w.st.Ping()
			if err != nil {
				return errors.Annotate(err, "state ping failed")
			}
		}
	}
}

// Kill is part of the worker.Worker interface.
func (w *stateWorker) Kill() {
	w.tomb.Kill(nil)
}

// Wait is part of the worker.Worker interface.
func (w *stateWorker) Wait() error {
	return w.tomb.Wait()
}
