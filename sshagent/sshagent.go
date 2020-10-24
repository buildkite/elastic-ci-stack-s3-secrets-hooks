package sshagent

import "errors"

type Agent struct{}

func (a *Agent) Add(key []byte) error {
	return errors.New("TODO")
}

func (a *Agent) Pid() uint {
	return 0
}
