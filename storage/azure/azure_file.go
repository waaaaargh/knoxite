/*
 * knoxite
 *     Copyright (c) 2020, Matthias Hartmann <mahartma@mahartma.com>
 *
 *   For license see LICENSE
 */

package azure

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/Azure/azure-storage-file-go/azfile"

	"github.com/knoxite/knoxite"
)

// AzureFileStorage stores data on an Azure File Storage.
type AzureFileStorage struct {
	knoxite.StorageFilesystem
	url        url.URL
	endpoint   url.URL
	credential azfile.SharedKeyCredential
}

func init() {
	knoxite.RegisterStorageBackend(&AzureFileStorage{})
}

// NewBackend returns a AzureFileStorage backend.
// URL needs to be the Storage account file service URL endpoint (get it from
// the Azure portal).
func (*AzureFileStorage) NewBackend(u url.URL) (knoxite.Backend, error) {
	if u.User == nil || u.User.Username() == "" {
		return &AzureFileStorage{}, knoxite.ErrInvalidUsername
	}
	pw, pwexist := u.User.Password()
	if !pwexist {
		return &AzureFileStorage{}, knoxite.ErrInvalidPassword
	}

	// user is the Azure accountName, password is the Azure accountKey
	credential, err := azfile.NewSharedKeyCredential(u.User.Username(), pw)
	if err != nil {
		return &AzureFileStorage{}, err
	}

	pp := strings.Split(u.Path, "/")
	share := pp[1]
	folder := "/" + strings.Join(pp[2:], "/")

	// adds Storage account name to endpoint if not provided
	hostname := u.Hostname()
	if !strings.HasPrefix(hostname, u.User.Username()+".") {
		hostname = u.User.Username() + "." + u.Hostname()
	}
	sURL, err := url.Parse(fmt.Sprintf("https://%s/%s", hostname, share))
	if err != nil {
		return &AzureFileStorage{}, knoxite.ErrInvalidRepositoryURL
	}

	backend := AzureFileStorage{
		endpoint:   *sURL,
		url:        u,
		credential: *credential,
	}

	fs, err := knoxite.NewStorageFilesystem(folder, &backend)
	if err != nil {
		return &AzureFileStorage{}, err
	}
	backend.StorageFilesystem = fs

	return &backend, nil
}

// Location returns the type and location of the repository.
func (backend *AzureFileStorage) Location() string {
	return backend.url.String()
}

// Close the backend.
func (backend *AzureFileStorage) Close() error {
	return nil
}

// Protocols returns the Protocol Schemes supported by this backend.
func (backend *AzureFileStorage) Protocols() []string {
	return []string{"azurefile"}
}

// Description returns a user-friendly description for this backend.
func (backend *AzureFileStorage) Description() string {
	return "Azure file storage"
}

// AvailableSpace returns the free space on this backend.
func (backend *AzureFileStorage) AvailableSpace() (uint64, error) {
	shareUrl := azfile.NewShareURL(backend.endpoint, azfile.NewPipeline(&backend.credential, azfile.PipelineOptions{}))
	props, err := shareUrl.GetProperties(context.Background())
	if err != nil {
		return 0, err
	}

	stats, err := shareUrl.GetStatistics(context.Background())
	if err != nil {
		return 0, err
	}

	gb := uint64(1 << (10 * 3))
	return uint64(props.Quota())*gb - uint64(stats.ShareUsageBytes), nil
}

// CreatePath creates a dir including all its parent dirs, when required.
func (backend *AzureFileStorage) CreatePath(p string) error {
	p = strings.TrimPrefix(p, "/")
	p = strings.TrimSuffix(p, "/")
	slicedPath := strings.Split(p, "/")

	u := backend.endpoint
	for _, v := range slicedPath {
		u.Path = path.Join(u.Path, v)

		directoryUrl := azfile.NewDirectoryURL(u, azfile.NewPipeline(&backend.credential, azfile.PipelineOptions{}))
		_, err := directoryUrl.Create(context.Background(), azfile.Metadata{
			"createdby": "knoxite",
		}, azfile.SMBProperties{})
		if err != nil && err.(azfile.StorageError).ServiceCode() != azfile.ServiceCodeResourceAlreadyExists {
			return err
		}
	}

	return nil
}

// Stat returns the size of a file.
func (backend *AzureFileStorage) Stat(p string) (uint64, error) {
	u := backend.endpoint
	u.Path = path.Join(u.Path, p)

	// we assume the share & file do already exist
	fileUrl := azfile.NewFileURL(u, azfile.NewPipeline(&backend.credential, azfile.PipelineOptions{}))
	props, err := fileUrl.GetProperties(context.Background())
	if err != nil {
		return 0, err
	}

	return uint64(props.ContentLength()), nil
}

// ReadFile reads a file from Azure file storage.
func (backend *AzureFileStorage) ReadFile(p string) ([]byte, error) {
	u := backend.endpoint
	u.Path = path.Join(u.Path, p)

	size, err := backend.Stat(p)
	if err != nil {
		return nil, err
	}

	bytes := make([]byte, size)
	fileUrl := azfile.NewFileURL(u, azfile.NewPipeline(&backend.credential, azfile.PipelineOptions{}))

	_, err = azfile.DownloadAzureFileToBuffer(context.Background(), fileUrl, bytes, azfile.DownloadFromAzureFileOptions{Parallelism: 1})
	if err != nil {
		return nil, err
	}

	return bytes, nil
}

// WriteFile writes a file on Azure file storage.
func (backend *AzureFileStorage) WriteFile(p string, data []byte) (size uint64, err error) {
	u := backend.endpoint
	u.Path = path.Join(u.Path, p)

	// we assume the share & file do already exist
	fileUrl := azfile.NewFileURL(u, azfile.NewPipeline(&backend.credential, azfile.PipelineOptions{}))

	err = azfile.UploadBufferToAzureFile(context.Background(), data, fileUrl, azfile.UploadToAzureFileOptions{
		Metadata: azfile.Metadata{
			"createdby": "knoxite",
		},
	})
	if err != nil {
		return 0, err
	}
	return uint64(len(data)), nil
}

// DeleteFile deletes a file from Azure file storage.
func (backend *AzureFileStorage) DeleteFile(p string) error {
	u := backend.endpoint
	u.Path = path.Join(u.Path, p)

	// we assume the share & file do already exist
	_, err := azfile.NewFileURL(u, azfile.NewPipeline(&backend.credential, azfile.PipelineOptions{})).Delete(context.Background())
	if err != nil {
		return err
	}
	return nil
}
