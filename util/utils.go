package util

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/tmc/scp"
	"golang.org/x/crypto/ssh"
)

func CreateClientByKey(rsaPath, user, host string, port int) (*ssh.Client, error) {
	var hostKey ssh.PublicKey
	// A public key may be used to authenticate against the remote
	// server by using an unencrypted PEM-encoded private key file.
	//
	// If you have an encrypted private key, the crypto/x509 package
	// can be used to decrypt it.
	// rsaPath is like "/home/user/.ssh/id_rsa"
	key, err := ioutil.ReadFile(rsaPath)
	if err != nil {
		log.Fatalf("unable to read private key: %v", err)
	}

	// Create the Signer for this private key.
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		log.Fatalf("unable to parse private key: %v", err)
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			// Use the PublicKeys method for remote authentication.
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
	}
	// Connect to the remote server and perform the SSH handshake.
	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		log.Fatalf("unable to connect: %v", err)
		return nil, err
	}

	return client, nil
}

func CreateClientByPassword(user, host, password string, port int) (*ssh.Client, error) {
	auth := make([]ssh.AuthMethod, 0)
	auth = append(auth, ssh.Password(password))

	clientConfig := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // FIXME if needed, actually not secure
		Timeout:         30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	client, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// 拷贝文件
func Scp(client *ssh.Client, srcFile, destFile string) error {
	// create session
	session, err := client.NewSession()
	if err != nil {
		fmt.Println("create new session error")
		return err
	}
	defer session.Close()
	if err := scp.CopyPath(srcFile, destFile, session); err != nil {
		fmt.Println("scp file error")
		return err
	}
	return nil
}

// 在远程节点执行命令
func Run(client *ssh.Client, cmd string) error {
	// create session
	session, err := client.NewSession()
	if err != nil {
		fmt.Println("create new session error")
		return err
	}
	defer session.Close()

	if err := session.Run(cmd); err != nil {
		return err
	}
	return nil
}

// Output runs cmd on the remote host and returns its standard output.
func RunWithOutput(client *ssh.Client, cmd string) (string, error) {
	// create session
	session, err := client.NewSession()
	if err != nil {
		fmt.Println("create new session error")
		return "", err
	}
	defer session.Close()

	output, err := session.Output(cmd)
	if err != nil {
		return "", err
	}
	str := strings.Trim(string(output), "\n")
	return str, nil
}

// 判断文件夹是否存在
func PathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
