// Copyright 2016 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/juju/cmd"
	"github.com/juju/errors"

	"github.com/juju/juju/cmd/modelcmd"
	"github.com/juju/juju/juju/osenv"
	"github.com/juju/juju/jujuclient"
)

func newSwitchCommand() cmd.Command {
	cmd := &switchCommand{
		Store: jujuclient.NewFileClientStore(),
		ReadCurrentController:  modelcmd.ReadCurrentController,
		WriteCurrentController: modelcmd.WriteCurrentController,
	}
	cmd.RefreshModels = cmd.JujuCommandBase.RefreshModels
	return modelcmd.WrapBase(cmd)
}

type switchCommand struct {
	modelcmd.JujuCommandBase
	RefreshModels          func(jujuclient.ClientStore, string) error
	ReadCurrentController  func() (string, error)
	WriteCurrentController func(string) error

	Store  jujuclient.ClientStore
	Target string
}

var switchDoc = `
Switch to the specified model or controller.

If the name identifies controller, the client will switch to the
active model for that controller. Otherwise, the name must specify
either the name of a model within the active controller, or a
fully-qualified model with the format "controller:model".
`

func (c *switchCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "switch",
		Args:    "<controller>|<model>|<controller>:<model>",
		Purpose: "change the active Juju model",
		Doc:     switchDoc,
	}
}

func (c *switchCommand) Init(args []string) error {
	if len(args) == 0 {
		return errors.Errorf("missing controller or model name")
	}
	if err := cmd.CheckEmpty(args[1:]); err != nil {
		return err
	}
	c.Target = args[0]
	return nil
}

func (c *switchCommand) Run(ctx *cmd.Context) (resultErr error) {

	// Get the current name for logging the transition.
	currentControllerName, err := c.ReadCurrentController()
	if err != nil {
		return errors.Trace(err)
	}
	currentName, err := c.name(currentControllerName)
	if err != nil {
		return errors.Trace(err)
	}
	var newName string
	defer func() {
		if resultErr != nil {
			return
		}
		logSwitch(ctx, currentName, &newName)
	}()

	// Switch is an alternative way of dealing with environments than using
	// the JUJU_MODEL environment setting, and as such, doesn't play too well.
	// If JUJU_MODEL is set we should report that as the current environment,
	// and not allow switching when it is set.
	if env := os.Getenv(osenv.JujuModelEnvKey); env != "" {
		return errors.Errorf("cannot switch when JUJU_MODEL is overriding the model (set to %q)", env)
	}

	// If the name identifies a controller, then set that as the current one.
	if _, err := c.Store.ControllerByName(c.Target); err == nil {
		if c.Target == currentControllerName {
			newName = currentName
			return nil
		} else {
			newName, err = c.name(c.Target)
			if err != nil {
				return errors.Trace(err)
			}
			return errors.Trace(c.WriteCurrentController(c.Target))
		}
	} else if !errors.IsNotFound(err) {
		return errors.Trace(err)
	}

	// The target is not a controller, so check for a model with
	// the given name. The name can be qualified with the controller
	// name (<controller>:<model>), or unqualified; in the latter
	// case, the model must exist in the current controller.
	var controllerName, modelName string
	if i := strings.IndexRune(c.Target, ':'); i > 0 {
		controllerName, modelName = c.Target[:i], c.Target[i+1:]
		newName = c.Target
	} else {
		if currentControllerName == "" {
			return unknownSwitchTargetError(c.Target)
		}
		controllerName = currentControllerName
		modelName = c.Target
		newName = fmt.Sprintf("%s:%s", controllerName, modelName)
	}

	err = c.Store.SetCurrentModel(controllerName, modelName)
	if errors.IsNotFound(err) {
		// The model isn't known locally, so we must query the controller.
		if err := c.RefreshModels(c.Store, controllerName); err != nil {
			return errors.Annotate(err, "refreshing models cache")
		}
		err := c.Store.SetCurrentModel(controllerName, modelName)
		if errors.IsNotFound(err) {
			return unknownSwitchTargetError(c.Target)
		} else if err != nil {
			return errors.Trace(err)
		}
	} else if err != nil {
		return errors.Trace(err)
	}
	if currentControllerName != controllerName {
		if err := c.WriteCurrentController(controllerName); err != nil {
			return errors.Trace(err)
		}
	}
	return nil
}

func unknownSwitchTargetError(name string) error {
	return errors.Errorf("%q is not the name of a model or controller", name)
}

func logSwitch(ctx *cmd.Context, oldName string, newName *string) {
	if *newName == oldName {
		ctx.Infof("%s (no change)", oldName)
	} else {
		ctx.Infof("%s -> %s", oldName, *newName)
	}
}

// name returns the name of the current model for the specified controller
// if one is set, otherwise the controller name with an indicator that it
// is the name of a controller and not a model.
func (c *switchCommand) name(controllerName string) (string, error) {
	if controllerName == "" {
		return "", nil
	}
	modelName, err := c.Store.CurrentModel(controllerName)
	if err == nil {
		return fmt.Sprintf("%s:%s", controllerName, modelName), nil
	}
	if errors.IsNotFound(err) {
		return fmt.Sprintf("%s (controller)", controllerName), nil
	}
	return "", errors.Trace(err)
}
