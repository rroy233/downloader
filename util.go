package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

var clear map[string]func() //create a map for storing clear funcs

/*
	clear terminal method
	https://cloud.tencent.com/developer/ask/142335
*/
func initClear() {
	clear = make(map[string]func()) //Initialize it
	clear["linux"] = func() {
		cmd := exec.Command("clear") //Linux example, its tested
		cmd.Stdout = os.Stdout
		cmd.Run()
	}
	clear["darwin"] = func() {
		cmd := exec.Command("clear") //Linux example, its tested
		cmd.Stdout = os.Stdout
		cmd.Run()
	}
	clear["windows"] = func() {
		cmd := exec.Command("cmd", "/c", "cls") //Windows example, its tested
		cmd.Stdout = os.Stdout
		cmd.Run()
	}
}
func CallClear() {
	value, ok := clear[runtime.GOOS] //runtime.GOOS -> linux, windows, darwin etc.
	if ok {                          //if we defined a clear func for that platform:
		value() //we execute it
	}
}

func ReadInput(reader *bufio.Reader) (string, error) {
	input, isPrefix, err := reader.ReadLine()
	if err != nil {
		return "", err
	}
	if isPrefix == true {
		return "", errors.New("未检测到换行符")
	}
	return string(input), nil
}

func cleanTmpFile() {
	if len(tmpFileQueue) != 0 {
		dir := <-tmpFileQueue
		err := os.RemoveAll(dir)
		if err != nil {
			fmt.Printf("清除临时文件夹[%s]失败，请手动清理:%s", dir, err.Error())
			return
		}
		fmt.Println("已清理临时文件")
	}
}
