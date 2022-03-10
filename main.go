package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	url2 "net/url"
	"os"
	"os/signal"
	"strconv"
)

// MaxFileSize 文件最大大小
const MaxFileSize uint64 = 10 << 30 //10GB

// PieceSize 文件切片大小
const PieceSize uint64 = 50 << 20 //50MB

var tmpDir string
var tmpFileQueue chan string

func main() {
	//清屏
	initClear()
	var err error

	//临时目录清理队列，用于垃圾回收
	tmpFileQueue = make(chan string, 1)

	tmpDir, err = ioutil.TempDir("", "downloader_")
	if err != nil {
		fmt.Println("生成临时目录失败：" + err.Error())
		return
	}
	tmpFileQueue <- tmpDir

	go func() {
		inputReader := bufio.NewReader(os.Stdin)
		input := ""
		var task *downloadTask
		for {
			var url *url2.URL
			fmt.Println()
			fmt.Println("1、请输入下载地址(仅支持HTTP协议,按Control+C退出)：")
			input, err = ReadInput(inputReader)
			url, err = url2.Parse(input)
			if err != nil {
				fmt.Println("地址错误")
				continue
			}

			fmt.Println("2、请输入最大线程数(不宜超过32)：")
			input, err = ReadInput(inputReader)
			maxThread, err := strconv.Atoi(input)
			if err != nil {
				fmt.Println("输入无效")
				continue
			}

			task, err = NewTask(url, maxThread)
			if err != nil {
				fmt.Println(err.Error())
				continue
			}

			err = task.Download()
			if err != nil {
				fmt.Println(err.Error())
				continue
			}
		}
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, os.Kill)
	<-sigCh
	cleanTmpFile()
}
