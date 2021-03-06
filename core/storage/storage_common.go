package storage

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/pkg/errors"
	"github.com/rancher/agent/core/marshaller"
	"github.com/rancher/agent/core/progress"
	"github.com/rancher/agent/model"
	"github.com/rancher/agent/utilities/config"
	"github.com/rancher/agent/utilities/constants"
	"github.com/rancher/agent/utilities/utils"
	"golang.org/x/net/context"
	"os"
	"strings"
)

func isManagedVolume(volume model.Volume) bool {
	driver := volume.Data.Fields.Driver
	if driver == "" {
		return false
	}
	if volume.Name == "" {
		return false
	}
	return true
}

func imageBuild(image model.Image, progress *progress.Progress, dockerClient *client.Client) error {
	opts := image.Data.Fields.Build

	if opts.Context != "" {
		file, err := utils.DownloadFile(opts.Context, config.Builds(), nil, "")
		if err == nil {
			opts.FileObj = file
			if buildErr := doBuild(opts, progress, dockerClient); buildErr != nil {
				return errors.Wrap(buildErr, constants.ImageBuildError+"failed to build image")
			}
		}
		if file != "" {
			// ignore this error because we don't care if that file doesn't exist
			os.Remove(file)
		}
	} else {
		remote := opts.Remote
		if strings.HasPrefix(utils.InterfaceToString(remote), "git@github.com:") {
			remote = strings.Replace(utils.InterfaceToString(remote), "git@github.com:", "git://github.com/", -1)
		}
		opts.Remote = remote
		if buildErr := doBuild(opts, progress, dockerClient); buildErr != nil {
			return errors.Wrap(buildErr, constants.ImageBuildError+"failed to build image")
		}
	}
	return nil
}

func doBuild(opts model.BuildOptions, progress *progress.Progress, client *client.Client) error {
	remote := opts.Remote
	if remote == "" {
		remote = opts.Context
	}
	imageBuildOptions := types.ImageBuildOptions{
		RemoteContext: remote,
		Remove:        true,
		Tags:          []string{opts.Tag},
	}
	response, err := client.ImageBuild(context.Background(), nil, imageBuildOptions)
	if err != nil {
		return errors.Wrap(err, constants.DoBuildError+"failed to build image")
	}
	defer response.Body.Close()
	buffer := utils.ReadBuffer(response.Body)
	statusList := strings.Split(buffer, "\r\n")
	for _, rawStatus := range statusList {
		if rawStatus != "" {
			status := marshaller.FromString(rawStatus)
			if value, ok := utils.GetFieldsIfExist(status, "stream"); ok {
				progress.Update(utils.InterfaceToString(value), "yes", nil)
			}
		}
	}
	return nil
}

func isBuild(image model.Image) bool {
	build := image.Data.Fields.Build
	if build.Context != "" || build.Remote != "" {
		return true
	}
	return false
}

func pathToVolume(volume model.Volume) string {
	return strings.Replace(volume.URI, "file://", "", -1)
}

func pullImageWrap(client *client.Client, imageUUID string, opts types.ImagePullOptions, progress *progress.Progress) error {
	lastMessage := ""
	message := ""
	reader, err := client.ImagePull(context.Background(), imageUUID, opts)
	if err != nil {
		return errors.Wrap(err, "Failed to pull image")
	}
	defer reader.Close()
	buffer := utils.ReadBuffer(reader)
	statusList := strings.Split(buffer, "\r\n")
	for _, rawStatus := range statusList {
		if rawStatus != "" {
			status := marshaller.FromString(rawStatus)
			if utils.HasKey(status, "error") {
				return fmt.Errorf("Image [%s] failed to pull: %s", imageUUID, message)
			}
			if utils.HasKey(status, "status") {
				message = utils.InterfaceToString(status["status"])
			}
		}
		if lastMessage != message && progress != nil {
			progress.Update(message, "yes", nil)
			lastMessage = message
		}
	}
	return nil
}

func wrapAuth(auth types.AuthConfig) string {
	buf, err := json.Marshal(auth)
	if err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(buf)
}
