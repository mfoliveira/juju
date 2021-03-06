// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package common

import (
	"github.com/juju/errors"
	"github.com/juju/loggo"
	"gopkg.in/juju/names.v2"

	"github.com/juju/juju/apiserver/facade"
	"github.com/juju/juju/apiserver/params"
	"github.com/juju/juju/core/model"
	"github.com/juju/juju/state"
)

//go:generate mockgen -package mocks -destination mocks/mock_backend.go github.com/juju/juju/apiserver/common UpgradeSeriesBackend
type UpgradeSeriesBackend interface {
	Machine(string) (UpgradeSeriesMachine, error)
	Unit(string) (UpgradeSeriesUnit, error)
}

//go:generate mockgen -package mocks -destination mocks/mock_machine.go github.com/juju/juju/apiserver/common UpgradeSeriesMachine
type UpgradeSeriesMachine interface {
	WatchUpgradeSeriesNotifications() (state.NotifyWatcher, error)
	Units() ([]UpgradeSeriesUnit, error)
}

//go:generate mockgen -package mocks -destination mocks/mock_unit.go github.com/juju/juju/apiserver/common UpgradeSeriesUnit
type UpgradeSeriesUnit interface {
	Tag() names.Tag
	AssignedMachineId() (string, error)
	UpgradeSeriesStatus(model.UpgradeSeriesStatusType) (model.UnitSeriesUpgradeStatus, error)
	SetUpgradeSeriesStatus(model.UnitSeriesUpgradeStatus, model.UpgradeSeriesStatusType) error
}

type upgradeSeriesMachine struct {
	*state.Machine
}

// Units maintains the UpgradeSeriesMachine indirection by wrapping the call to
// state.Machine.Units().
func (m *upgradeSeriesMachine) Units() ([]UpgradeSeriesUnit, error) {
	units, err := m.Units()
	if err != nil {
		return nil, errors.Trace(err)
	}

	wrapped := make([]UpgradeSeriesUnit, len(units))
	for i, u := range units {
		wrapped[i] = u
	}
	return wrapped, nil
}

type UpgradeSeriesAPI struct {
	backend   UpgradeSeriesBackend
	resources facade.Resources

	logger loggo.Logger

	accessUnitOrMachine GetAuthFunc
	accessMachine       GetAuthFunc
	accessUnit          GetAuthFunc
}

// NewUpgradeSeriesAPI returns a new UpgradeSeriesAPI. Currently both
// GetAuthFuncs can used to determine current permissions.
func NewUpgradeSeriesAPI(
	backend UpgradeSeriesBackend,
	resources facade.Resources,
	authorizer facade.Authorizer,
	accessMachine GetAuthFunc,
	accessUnit GetAuthFunc,
	logger loggo.Logger,
) *UpgradeSeriesAPI {
	logger.Tracef("NewUpgradeSeriesAPI called with %s", authorizer.GetAuthTag())
	return &UpgradeSeriesAPI{
		backend:             backend,
		resources:           resources,
		accessUnitOrMachine: AuthAny(accessUnit, accessMachine),
		accessMachine:       accessMachine,
		accessUnit:          accessUnit,
		logger:              logger,
	}
}

// WatchUpgradeSeriesNotifications returns a NotifyWatcher for observing changes to upgrade series locks.
func (u *UpgradeSeriesAPI) WatchUpgradeSeriesNotifications(args params.Entities) (params.NotifyWatchResults, error) {
	u.logger.Tracef("Starting WatchUpgradeSeriesNotifications with %+v", args)
	result := params.NotifyWatchResults{
		Results: make([]params.NotifyWatchResult, len(args.Entities)),
	}
	canAccess, err := u.accessUnitOrMachine()
	if err != nil {
		return params.NotifyWatchResults{}, err
	}
	for i, entity := range args.Entities {
		tag, err := names.ParseTag(entity.Tag)
		if err != nil {
			result.Results[i].Error = ServerError(ErrPerm)
			continue
		}

		watcherId := ""
		if !canAccess(tag) {
			result.Results[i].Error = ServerError(ErrPerm)
			continue
		}
		machine, err := u.getMachine(tag)
		if err != nil {
			result.Results[i].Error = ServerError(err)
			continue
		}
		w, err := machine.WatchUpgradeSeriesNotifications()
		if err != nil {
			result.Results[i].Error = ServerError(err)
			continue
		}
		watcherId = u.resources.Register(w)
		result.Results[i].NotifyWatcherId = watcherId
	}
	return result, nil
}

// UpgradeSeriesPrepareStatus returns the current preparation status of an upgrading
// unit. If no series upgrade is in progress an error is returned instead.
func (u *UpgradeSeriesAPI) UpgradeSeriesPrepareStatus(args params.Entities) (params.UpgradeSeriesStatusResults, error) {
	return u.upgradeSeriesStatus(args, model.PrepareStatus)
}

// UpgradeSeriesCompleteStatus returns the current completion status of upgrading
// unit. If no series upgrade is in progress an error is returned instead.
func (u *UpgradeSeriesAPI) UpgradeSeriesCompleteStatus(args params.Entities) (params.UpgradeSeriesStatusResults, error) {
	return u.upgradeSeriesStatus(args, model.CompleteStatus)
}

// SetUpgradeSeriesPrepareStatus sets the upgrade series status of the unit.
// If no upgrade is in progress an error is returned instead.
func (u *UpgradeSeriesAPI) SetUpgradeSeriesPrepareStatus(args params.SetUpgradeSeriesStatusParams) (params.ErrorResults, error) {
	u.logger.Tracef("Starting SetUpgradeSeriesPrepareStatus with %+v", args)
	return u.setUpgradeSeriesStatus(args, model.PrepareStatus)
}

// SetUpgradeSeriesCompleteStatus sets the upgrade series status of the unit.
// If no upgrade is in progress an error is returned instead.
func (u *UpgradeSeriesAPI) SetUpgradeSeriesCompleteStatus(args params.SetUpgradeSeriesStatusParams) (params.ErrorResults, error) {
	u.logger.Tracef("Starting SetUpgradeSeriesCompleteStatus with %+v", args)
	return u.setUpgradeSeriesStatus(args, model.CompleteStatus)
}

func (u *UpgradeSeriesAPI) getMachine(tag names.Tag) (UpgradeSeriesMachine, error) {
	var id string
	switch tag.Kind() {
	case names.MachineTagKind:
		id = tag.Id()
	case names.UnitTagKind:
		unit, err := u.backend.Unit(tag.Id())
		if err != nil {

		}
		id, err = unit.AssignedMachineId()
		if err != nil {
			return nil, err
		}
	default:
	}
	return u.backend.Machine(id)
}

func (u *UpgradeSeriesAPI) getUnit(tag names.Tag) (UpgradeSeriesUnit, error) {
	return u.backend.Unit(tag.Id())
}

// NewExternalUpgradeSeriesAPI can be used for API registration.
func NewExternalUpgradeSeriesAPI(
	st *state.State,
	resources facade.Resources,
	authorizer facade.Authorizer,
	accessMachine GetAuthFunc,
	accessUnit GetAuthFunc,
	logger loggo.Logger,
) *UpgradeSeriesAPI {
	return NewUpgradeSeriesAPI(backendShim{st}, resources, authorizer, accessMachine, accessUnit, logger)
}

func (u *UpgradeSeriesAPI) setUpgradeSeriesStatus(args params.SetUpgradeSeriesStatusParams, statusType model.UpgradeSeriesStatusType) (params.ErrorResults, error) {
	result := params.ErrorResults{
		Results: make([]params.ErrorResult, len(args.Params)),
	}
	canAccess, err := u.accessUnit()
	if err != nil {
		return params.ErrorResults{}, err
	}
	for i, p := range args.Params {
		//TODO[externalreality] refactor all of this, its being copied often.
		tag, err := names.ParseUnitTag(p.Entity.Tag)
		if err != nil {
			result.Results[i].Error = ServerError(ErrPerm)
			continue
		}
		if !canAccess(tag) {
			result.Results[i].Error = ServerError(ErrPerm)
			continue
		}
		unit, err := u.getUnit(tag)
		if err != nil {
			result.Results[i].Error = ServerError(err)
			continue
		}
		status, err := model.ValidateUnitSeriesUpgradeStatus(p.Status)
		if err != nil {
			result.Results[i].Error = ServerError(err)
			continue
		}
		err = unit.SetUpgradeSeriesStatus(status, statusType)
		if err != nil {
			result.Results[i].Error = ServerError(err)
			continue
		}
	}
	return result, nil
}

func (u *UpgradeSeriesAPI) upgradeSeriesStatus(
	args params.Entities, statusType model.UpgradeSeriesStatusType,
) (params.UpgradeSeriesStatusResults, error) {
	u.logger.Tracef("Starting UpgradeSeriesPrepareStatus with %+v", args)
	result := params.UpgradeSeriesStatusResults{}

	canAccess, err := u.accessUnitOrMachine()
	if err != nil {
		return result, err
	}

	for _, entity := range args.Entities {
		tag, err := names.ParseTag(entity.Tag)
		if err != nil {
			result.Results = append(result.Results, params.UpgradeSeriesStatusResult{Error: ServerError(err)})
			continue
		}
		if !canAccess(tag) {
			result.Results = append(result.Results, params.UpgradeSeriesStatusResult{Error: ServerError(ErrPerm)})
			continue
		}
		switch tag.Kind() {
		case names.MachineTagKind:
			// TODO (manadart 2018-08-01) If multiple machine entities are
			// passed in the call, the return will not distinguish between
			// What unit status results belong to which machine.
			// At this stage we do not anticipate this, so... YAGNI.
			result.Results = append(result.Results, u.upgradeSeriesMachineStatus(tag, statusType)...)
		case names.UnitTagKind:
			result.Results = append(result.Results, u.upgradeSeriesUnitStatus(tag, statusType))
		}
	}

	return result, nil
}

// upgradeSeriesMachineStatus returns a result containing the upgrade-series
// status of all units managed buy the input machine, for the input status type.
func (u *UpgradeSeriesAPI) upgradeSeriesMachineStatus(
	machineTag names.Tag, statusType model.UpgradeSeriesStatusType,
) []params.UpgradeSeriesStatusResult {
	machine, err := u.getMachine(machineTag)
	if err != nil {
		return []params.UpgradeSeriesStatusResult{{Error: ServerError(err)}}
	}

	units, err := machine.Units()
	if err != nil {
		return []params.UpgradeSeriesStatusResult{{Error: ServerError(err)}}
	}

	results := make([]params.UpgradeSeriesStatusResult, len(units))
	for i, unit := range units {
		results[i] = u.upgradeSeriesUnitStatus(unit.Tag(), statusType)
	}
	return results
}

// upgradeSeriesUnitStatus returns a result containing the upgrade-series
// status of the input unit, for the input status type.
func (u *UpgradeSeriesAPI) upgradeSeriesUnitStatus(
	unitTag names.Tag, statusType model.UpgradeSeriesStatusType,
) params.UpgradeSeriesStatusResult {
	result := params.UpgradeSeriesStatusResult{}

	unit, err := u.getUnit(unitTag)
	if err != nil {
		result.Error = ServerError(err)
		return result
	}

	status, err := unit.UpgradeSeriesStatus(statusType)
	if err != nil {
		result.Error = ServerError(err)
		return result
	}

	result.Status = string(status)
	return result
}

type backendShim struct {
	st *state.State
}

func (shim backendShim) Machine(id string) (UpgradeSeriesMachine, error) {
	m, err := shim.st.Machine(id)
	return &upgradeSeriesMachine{m}, err
}

func (shim backendShim) Unit(id string) (UpgradeSeriesUnit, error) {
	return shim.st.Unit(id)
}
