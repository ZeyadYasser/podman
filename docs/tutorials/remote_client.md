# Podman remote-client tutorial

## Introduction
The purpose of the Podman remote-client is to allow users to interact with a Podman "backend" while on a separate client.  The command line interface of the remote client is exactly the same as the regular Podman commands with the exception of some flags being removed as they do not apply to the remote-client.

The remote client takes advantage of a client-server model. You need Podman installed on a Linux machine or VM that also has the SSH daemon running. On the local operating system, when you execute a Podman command, Podman connects to the server via SSH. It then connects to the Podman service by using systemd socket activation, and hitting our [Rest API](https://docs.podman.io/en/latest/_static/api.html). The Podman commands are executed on the server. From the client's point of view, it seems like Podman runs locally.

This tutorial is for running Podman remotely on Linux. If you are using a Mac or a Windows PC, please follow the [Mac and Windows tutorial](https://github.com/containers/podman/blob/master/docs/tutorials/mac_win_client.md)

## Obtaining and installing Podman

### Client machine
You will need either Podman or the podman-remote client. The difference between the two is that the compiled podman-remote client can only act as a remote client connecting to a backend, while Podman can run local, standard Podman commands, as well as act as a remote client (using `podman --remote`)

If you already have Podman installed, you do not need to install podman-remote.

You can find out how to [install Podman here](https://podman.io/getting-started/installation)

If you would like to install only the podman-remote client, it is downloadable from its [release description page](https://github.com/containers/podman/releases/latest).  You can also build it from source using the `make podman-remote`


### Server Machine
You will need to [install Podman](https://podman.io/getting-started/installation) on your server machine.


## Creating the first connection

### Enable the Podman service on the server machine.

Before performing any Podman client commands, you must enable the podman.sock SystemD service on the Linux server.  In these examples, we are running Podman as a normal, unprivileged user, also known as a rootless user.  By default, the rootless socket listens at `/run/user/${UID}/podman/podman.sock`.  You can enable this socket permanently using the following command:
```
systemctl --user enable --now podman.socket
```
You will need to enable linger for this user in order for the socket to work when the user is not logged in:

```
sudo loginctl enable-linger $USER
```
This is only required if you are not running Podman as root.

You can verify that the socket is listening with a simple Podman command.

```
podman --remote info
host:
  arch: amd64
  buildahVersion: 1.16.0-dev
  cgroupVersion: v2
  conmon:
	package: conmon-2.0.19-1.fc32.x86_64
```

#### Enable sshd

In order for the Podman client to communicate with the server you need to enable and start the SSH daemon on your Linux machine, if it is not currently enabled.
```
sudo systemctl enable --now -s sshd
```

#### Setting up SSH
Remote Podman uses SSH to communicate between the client and server. The remote client works considerably smoother using SSH keys. To set up your ssh connection, you need to generate an ssh key pair from your client machine.
```
ssh-keygen
```
Your public key by default should be in your home directory under ~/.ssh/id_rsa.pub. You then need to copy the contents of id_rsa.pub and append it into  ~/.ssh/authorized_keys on the Linux  server. You can automate this using ssh-copy-id.

If you do not wish to use SSH keys, you will be prompted with each Podman command for your login password.

## Using the client

Note: `podman-remote` is equivalent to `podman --remote` here, depending on what you have chosen to install.

The first step in using the Podman remote client is to configure a connection.

You can add a connection by using the `podman-remote system connection add` command.

```
podman-remote system connection add myuser --identity ~/.ssh/id_rsa ssh://192.168.122.1/run/user/1000/podman/podman.sock
```

This will add a remote connection to Podman and if it is the first connection added, it will mark the connection as the default.  You can observe your connections with `podman-remote system connection list`:

```
podman-remote system connection list
Name	  Identity 	       URI
myuser*	  id_rsa	       ssh://myuser@192.168.122.1/run/user/1000/podman/podman.sock
```

Now we can test the connection with `podman info`:

```
podman-remote info
host:
  arch: amd64
  buildahVersion: 1.16.0-dev
  cgroupVersion: v2
  conmon:
	package: conmon-2.0.19-1.fc32.x86_64
```

Podman-remote has also introduced a “--connection” flag where you can use other connections you have defined.  If no connection is provided, the default connection will be used.

```
podman-remote system connection --help
```

## Wrap up

You can use the Podman remote clients to manage your containers running on a Linux server.  The communication between client and server relies heavily on SSH connections and the use of SSH keys are encouraged.  Once you have Podman installed on your remote client, you should set up a connection using `podman-remote system connection add` which will then be used by subsequent Podman commands.

## History
Adapted from the [Mac and Windows tutorial](https://github.com/containers/podman/blob/master/docs/tutorials/mac_win_client.md)