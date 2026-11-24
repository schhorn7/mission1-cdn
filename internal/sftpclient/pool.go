package sftpclient

import (
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/schhorn7/mission1-cdn/internal/models"
	"golang.org/x/crypto/ssh"
)

type RemoteFileStream struct {
	file 		*sftp.File
	sftpClient 	*sftp.Client
	sshClient	*ssh.Client
	size		int64
}

func (r *RemoteFileStream) Read(p []byte) (n int, err error) {
	return r.file.Read(p)
}

func (r *RemoteFileStream) Close() error {
	var errs []error
	if r.file != nil {
		if err := r.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.sftpClient != nil {
		if err := r.sftpClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.sshClient != nil {
		if err := r.sftpClient.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing remote stream: %v", errs)
	}
	return nil
}

func (r *RemoteFileStream) Size() int64 {
	return r.size
}

type ClientPool struct {
	mu sync.RWMutex
}

func NewClientPool() *ClientPool {
	return &ClientPool{}
}

func (p *ClientPool) getSSHConfig(node models.StorageNode) (*ssh.ClientConfig, error) {
	var authMethods []ssh.AuthMethod

	if node.Password != "" {
		authMethods = append(authMethods, ssh.Password(node.Password))
	}

	if node.KeyPath != "" {
		keyBytes, err := os.ReadFile(node.KeyPath)
		if err == nil {
			signer, err := ssh.ParsePrivateKey(keyBytes)
			if err == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	if node.KeyContent != "" {
		signer, err := ssh.ParsePrivateKey([]byte(node.KeyContent))
		if err == nil {
			authMethods = append(authMethods, ssh.PublicKeys(signer))
		}
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication method provided for node %s", node.NodeID)
	}

	return &ssh.ClientConfig{
		User: 			node.User,
		Auth: 			authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout: 		8 * time.Second,			
	}, nil
}

func (p *ClientPool) Dial(node models.StorageNode) (*ssh.Client, *sftp.Client, error) {
	config, err := p.getSSHConfig(node)
	if err != nil {
		return nil, nil, err
	}

	addr := net.JoinHostPort(node.IP, fmt.Sprintf("%d", node.Port))
	sshClient, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh dial to %s failed: %w", addr, err)
	}

	sftpClient, err := sftp.NewClient(sshClient)
	if err != nil {
		sshClient.Close()
		return nil, nil, fmt.Errorf("sftp init to %s failed: %w", addr, err)
	}

	return sshClient, sftpClient, nil
}

func (p *ClientPool) TestConnection(node models.StorageNode) error {
	sshClient, sftpClient, err := p.Dial(node)
	if err != nil {
		return err
	}
	defer sshClient.Close()
	defer sftpClient.Close()

	storageDir := node.StoragePath
	if storageDir == "" {
		storageDir = "/data/storage"
	}

	// Verify or create storage directory
	if err := sftpClient.MkdirAll(storageDir); err != nil {
		return fmt.Errorf("failed to verify storage dir %s: %w", storageDir, err)
	}

	return nil
}

func (p *ClientPool) WriteFile(node models.StorageNode, filename string, r io.Reader) (int64, error) {
	sshClient, sftpClient, err := p.Dial(node)
	if err != nil {
		return 0, err
	}
	defer sshClient.Close()
	defer sftpClient.Close()

	storageDir := node.StoragePath
	if storageDir == "" {
		storageDir = "/data/storage"
	}

	if err := sftpClient.MkdirAll(storageDir); err != nil {
		return 0, fmt.Errorf("cannot create remote dir: %w", err)
	}

	remoteFilePath := path.Join(storageDir, filename)
	dstFile, err := sftpClient.Create(remoteFilePath)
	if err != nil {
		return 0, fmt.Errorf("cannot create remote file %s: %w", remoteFilePath, err)
	}
	defer dstFile.Close()

	written, err := io.Copy(dstFile, r)
	if err != nil {
		return written, fmt.Errorf("failed to write stream to %s: %w", remoteFilePath, err)
	}

	return written, nil
}

func (p *ClientPool) ReadFile(node models.StorageNode, filename string) (*RemoteFileStream, error) {
	sshClient, sftpClient, err := p.Dial(node)
	if err != nil {
		return nil, err
	}

	storageDir := node.StoragePath
	if storageDir == "" {
		storageDir = "/data/storage"
	}

	remoteFilePath := path.Join(storageDir, filename)
	file, err := sftpClient.Open(remoteFilePath)
	if err != nil {
		sftpClient.Close()
		sshClient.Close()
		return nil, fmt.Errorf("failed to open remote file %s: %w", remoteFilePath, err)
	}

	stat, err := file.Stat()
	var size int64
	if err == nil {
		size = stat.Size()
	}

	return &RemoteFileStream{
		file:       file,
		sftpClient: sftpClient,
		sshClient:  sshClient,
		size:       size,
	}, nil
}
