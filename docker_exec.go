package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/docker/docker/api/types"
)

func dockerExec(ctx context.Context, name string, cmd []string, timeout time.Duration, previousExecID string) (string, error) {
	if previousExecID != "" && isExecRunning(ctx, name, previousExecID) {
		return previousExecID, attachExec(ctx, name, previousExecID, timeout)
	}
	execID, err := createExecConfig(ctx, name, cmd)
	if err != nil {
		return "", err
	}
	err = attachExec(ctx, name, execID, timeout)
	if err == nil {
		execID = ""
	}
	return execID, err
}

func isExecRunning(ctx context.Context, name, execID string) bool {
	res, _ := cli.ContainerExecInspect(ctx, execID)
	return res.Running
}

func createExecConfig(ctx context.Context, name string, cmd []string) (string, error) {
	execConfig := types.ExecConfig{
		Cmd: cmd,
		// One of the output streams must be attached in order to make ContainerExecAttach method wait for exit.
		AttachStderr: true,
	}
	resp, err := cli.ContainerExecCreate(ctx, name, execConfig)
	if err != nil {
		return "", err
	}
	// Not sure if it can return an empty ID but the check was present on Docker CLI code so I copied it.
	// https://github.com/docker/cli/blob/fe93451cf7d26fd211c4f4c2f55d32022294b628/cli/command/container/exec.go#L101-L104
	if resp.ID == "" {
		return "", errors.New("exec ID empty")
	}
	return resp.ID, nil
}

func attachExec(ctx context.Context, name string, execID string, timeout time.Duration) error {
	resp, err := cli.ContainerExecAttach(ctx, execID, types.ExecStartCheck{})
	if err != nil {
		return err
	}
	defer resp.Close()
	// Response connection stays open until the check command exits.
	// It could be stuck and we don't want to wait forever. That's why we put a deadline on read.
	err = resp.Conn.SetReadDeadline(time.Now().Add(timeout))
	if err != nil {
		return err
	}
	// Stream stays open until the check command exits.
	_, err = io.Copy(io.Discard, resp.Reader)
	if err != nil {
		return err
	}
	// Following call uses a new context because ctx could be timed out if the check command has run longer than CheckTimeout.
	res, err := cli.ContainerExecInspect(context.Background(), execID)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("check command exited with code: %d", res.ExitCode)
	}
	return nil
}
