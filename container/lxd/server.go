// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package lxd

import (
	"github.com/juju/errors"
	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
)

// Server extends the upstream LXD container server.
type Server struct {
	lxd.ContainerServer

	name      string
	clustered bool

	networkAPISupport bool
	clusterAPISupport bool

	localBridgeName string
}

// NewServer builds and returns a Server for high-level interaction with the
// input LXD container server.
func NewServer(svr lxd.ContainerServer) *Server {
	info, _, _ := svr.GetServer()
	apiExt := info.APIExtensions

	name := info.Environment.ServerName
	clustered := info.Environment.ServerClustered
	if clustered {
		logger.Debugf("creating LXD server for cluster node %q", name)
	}

	return &Server{
		ContainerServer:   svr,
		name:              name,
		clustered:         clustered,
		networkAPISupport: shared.StringInSlice("network", apiExt),
		clusterAPISupport: shared.StringInSlice("clustering", apiExt),
	}
}

// UpdateServerConfig updates the server configuration with the input values.
func (s *Server) UpdateServerConfig(cfg map[string]string) error {
	svr, eTag, err := s.GetServer()
	if err != nil {
		return errors.Trace(err)
	}
	if svr.Config == nil {
		svr.Config = make(map[string]interface{})
	}
	for k, v := range cfg {
		svr.Config[k] = v
	}
	return errors.Trace(s.UpdateServer(svr.Writable(), eTag))
}

// UpdateContainerConfig updates the configuration for the container with the
// input name, using the input values.
func (s *Server) UpdateContainerConfig(name string, cfg map[string]string) error {
	container, eTag, err := s.GetContainer(name)
	if err != nil {
		return errors.Trace(err)
	}
	if container.Config == nil {
		container.Config = make(map[string]string)
	}
	for k, v := range cfg {
		container.Config[k] = v
	}

	resp, err := s.UpdateContainer(name, container.Writable(), eTag)
	if err != nil {
		return errors.Trace(err)
	}
	return errors.Trace(resp.Wait())
}

// CreateClientCertificate adds the input certificate to the server,
// indicating that is for use in client communication.
func (s *Server) CreateClientCertificate(cert *Certificate) error {
	req, err := cert.AsCreateRequest()
	if err != nil {
		return errors.Trace(err)
	}
	return errors.Trace(s.CreateCertificate(req))
}

// IsLXDNotFound checks if an error from the LXD API indicates that a requested
// entity was not found.
func IsLXDNotFound(err error) bool {
	return err != nil && err.Error() == "not found"
}
