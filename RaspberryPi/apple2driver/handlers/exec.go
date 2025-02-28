// Copyright Terence J. Boldt (c)2020-2022
// Use of this source code is governed by an MIT
// license that can be found in the LICENSE file.

// This file contains the handler for executing Linux and internal
// commands

package handlers

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/tjboldt/Apple2-IO-RPi/RaspberryPi/apple2driver/info"
)

var forceLowercase = false

// ExecCommand handles requests for the Apple II executing Linux commands
func ExecCommand() {
	workingDirectory, err := os.Getwd()
	if err != nil {
		workingDirectory = "/home"
		comm.WriteString("Failed to get current working directory, setting to /home\r")
	}

	fmt.Printf("Reading command to execute...\n")
	linuxCommand, err := comm.ReadString()
	if forceLowercase {
		linuxCommand = strings.ToLower(linuxCommand)
	}
	linuxCommand = strings.Trim(linuxCommand, " ")
	if linuxCommand == "" {
		linuxCommand = "a2help"
	}
	fmt.Printf("Command to run: %s\n", linuxCommand)
	if strings.HasPrefix(linuxCommand, "cd ") {
		workingDirectory = strings.Replace(linuxCommand, "cd ", "", 1)
		err = os.Chdir(workingDirectory)
		if err != nil {
			comm.WriteString("Failed to set working directory\r")
			return
		}
		comm.WriteString("Working directory set\r")
		return
	}
	if linuxCommand == "a2version" {
		a2version()
		return
	}
	if linuxCommand == "a2help" {
		a2help()
		return
	}
	if linuxCommand == "a2lower" {
		a2lower(false)
		return
	}
	if linuxCommand == "A2LOWER" {
		a2lower(true)
		return
	}
	if linuxCommand == "a2wifi" {
		a2wifi()
		return
	}
	if linuxCommand == "a2prompt" {
		prompt := fmt.Sprintf("A2IO:%s ", workingDirectory)
		comm.WriteString(prompt)
		return
	}
	if linuxCommand == "a2wifi list" {
		linuxCommand = a2wifiList()
	}
	if strings.HasPrefix(linuxCommand, "a2wifi select") {
		linuxCommand, err = a2wifiSelect(linuxCommand)
	}
	if err == nil {
		execCommand(linuxCommand, workingDirectory)
	}
}

func execCommand(linuxCommand string, workingDirectory string) {
	// force the command to combine stderr(2) into stdout(1)
	linuxCommand += " 2>&1"
	cmd := exec.Command("bash", "-c", linuxCommand)
	cmd.Dir = workingDirectory
	cmd.Env = append(os.Environ(),
		"TERM=vt100",
		"LINES=24",
		"COLUMNS=80",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Printf("Failed to set stdout\n")
		comm.WriteString("Failed to set stdout\r")
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Printf("Failed to set stdin\n")
		comm.WriteString("Failed to set stdin\r")
		return
	}

	fmt.Printf("Command output:\n")
	err = cmd.Start()
	if err != nil {
		fmt.Printf("Failed to start command\n")
		comm.WriteString("Failed to start command\r")
		return
	}

	outputComplete := make(chan bool)
	inputComplete := make(chan bool)
	userCancelled := make(chan bool)

	go getStdin(stdin, outputComplete, inputComplete, userCancelled)
	go getStdout(stdout, outputComplete, userCancelled)

	for {
		select {
		case <-outputComplete:
			outputComplete <- true
			cmd.Wait()
			comm.WriteByte(0)
			return
		case <-userCancelled:
			userCancelled <- true
			cmd.Process.Kill()
			return
		case <-inputComplete:
			cmd.Wait()
			comm.WriteByte(0)
			return
		}
	}
}

func getStdout(stdout io.ReadCloser, outputComplete chan bool, userCancelled chan bool) {
	for {
		select {
		case <-userCancelled:
			fmt.Printf("User Cancelled stdout\n")
			stdout.Close()
			return
		default:
			bb := make([]byte, 1)
			n, err := stdout.Read(bb)
			if err != nil {
				stdout.Close()
				outputComplete <- true
				return
			}
			if n > 0 {
				b := bb[0]
				comm.SendCharacter(b)
			}
		}
	}
}

func getStdin(stdin io.WriteCloser, done chan bool, inputComplete chan bool, userCancelled chan bool) {
	for {
		select {
		case <-done:
			stdin.Close()
			inputComplete <- true
			return
		default:
			b, err := comm.ReadByte()
			if err == nil {
				if b == 0x00 || b == 0x03 {
					stdin.Close()
					userCancelled <- true
					fmt.Printf("\nUser cancelled stdin\n")
					return
				}
				bb := make([]byte, 1)
				stdin.Write(bb)
			}
		}
	}
}

func a2version() {
	comm.WriteString("\rVersion: " + info.Version + "\r")
}

func a2help() {
	comm.WriteString("\r" +
		"Built-in commands:\r" +
		"------------------\r" +
		"a2version - display version number\r" +
		"a2help - display this message\r" +
		"a2wifi - set up wifi\r" +
		"A2LOWER - force lowercase for II+\r" +
		"a2lower - disable force lowercase for II+\r" +
		"\r")
}

func a2lower(enable bool) {
	forceLowercase = enable
	comm.WriteString(fmt.Sprintf("All commands will be converted to lowercase: %t\r", forceLowercase))
}

func a2wifi() {
	comm.WriteString("\r" +
		"Usage: a2wifi list\r" +
		"       a2wifi select SSID PASSWORD REGION\r" +
		"\r")
}

func a2wifiList() string {
	return "sudo iwlist wlan0 scanning | grep ESSID | sed s/.*ESSID://g | sed s/\\\"//g"
}

func a2wifiSelect(linuxCommand string) (string, error) {
	params := strings.Fields(linuxCommand)
	if len(params) != 5 {
		comm.WriteString("\rIncorrect number of parameters. Usage: a2wifi select SSID PASSWORD REGION\r\r")
		return "", errors.New("Incorrect number of parameters. Usage: a2wifi select SSID PASSWORD REGION")
	}
	ssid := params[2]
	psk := params[3]
	region := params[4]
	linuxCommand = "printf \"country=%s\\nupdate_config=1\\nctrl_interface=/var/run/wpa_supplicant\\n\\nnetwork={\\n  scan_ssid=1\\n  ssid=\\\"%s\\\"\n  psk=\\\"%s\\\"\\n}\\n\" " +
		region + " " +
		ssid + " " +
		psk + " " +
		" > /tmp/wpa_supplicant.conf; " +
		"sudo mv /tmp/wpa_supplicant.conf /etc/wpa_supplicant/; " +
		"sudo wpa_cli -i wlan0 reconfigure"
	return linuxCommand, nil
}
