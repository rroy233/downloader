package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	url2 "net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type downloadTask struct {
	Url                   *url2.URL
	SavePath              string
	FileName              string
	Size                  uint64
	Downloaded            uint64
	MaxThreadNum          int
	SupportMultipleThread bool
	File                  []filePart
}

type filePart struct {
	ID          int
	TmpFilePath string
	Finished    bool
	Begin       uint64
	End         uint64
}

func NewTask(url *url2.URL, maxThreadNum int) (*downloadTask, error) {
	if maxThreadNum < 0 {
		return nil, errors.New("线程数不合法")
	}

	client := &http.Client{}
	client.Timeout = 15 * time.Second
	req := &http.Request{
		Method: http.MethodHead,
		URL:    url,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//获取文件大小
	if resp.Header.Get("Content-Length") == "" {
		return nil, errors.New("请打开浏览器下载此文件")
	}
	size, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return nil, err
	}
	if uint64(size) > MaxFileSize {
		return nil, errors.New("文件大小超过支持的最大值")
	}
	fmt.Println("总大小：", size>>20, "MB")

	//生成存储路径
	fileName := "newFile"
	urlParts := strings.Split(url.Path, "/")
	if len(urlParts) != 0 {
		if strings.Contains(urlParts[len(urlParts)-1], ".") == true {
			fileName = urlParts[len(urlParts)-1]
		}
	}

	support := true
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		maxThreadNum = 1
		fmt.Println("不支持多线程下载，仅启用一个线程")
		support = false
	}

	//切片数量
	pieceNum := uint64(size)/PieceSize + 1
	if support == false {
		pieceNum = 1
	}

	//初始化每个切片
	filePart := make([]filePart, pieceNum)
	if support == false {
		filePart[0].ID = 1
		filePart[0].TmpFilePath = fmt.Sprintf("%s/%s_%d", tmpDir, fileName, 0)
		filePart[0].Begin = 0
		filePart[0].End = uint64(size) - 1
		filePart[0].Finished = false
	} else {
		bytesLast := uint64(size)
		bytesBegin := uint64(0)
		for i, _ := range filePart {
			filePart[i].ID = i + 1
			filePart[i].TmpFilePath = fmt.Sprintf("%s/%s_%d", tmpDir, fileName, i)
			filePart[i].Begin = bytesBegin
			if bytesLast < PieceSize {
				filePart[i].End = bytesBegin + bytesLast - 1
			} else {
				filePart[i].End = bytesBegin + PieceSize - 1
			}

			bytesBegin = filePart[i].End + 1
			bytesLast -= filePart[i].End - filePart[i].Begin + 1
			filePart[i].Finished = false
		}
	}

	return &downloadTask{
		Url:                   url,
		SavePath:              "./" + fileName,
		FileName:              fileName,
		MaxThreadNum:          maxThreadNum,
		Size:                  uint64(size),
		File:                  filePart,
		SupportMultipleThread: support,
	}, nil
}

func (task *downloadTask) Download() error {
	stopCtx, cancel := context.WithCancel(context.Background())
	downloadQueue := make(chan *filePart, task.MaxThreadNum)
	okChan := make(chan int, task.MaxThreadNum)

	//启动协程
	for i := 0; i < task.MaxThreadNum; i++ {
		go downloader(stopCtx, downloadQueue, i, okChan, task)
	}

	//将个各个切片插入队列
	go downloadTaskDispatcher(stopCtx, task, downloadQueue)

	//显示下载进度
	go showDownloadProgress(stopCtx, task)

	//等待所有任务完成
	completedNum := 0
	errNum := 0
	var downloadErr error
	for {
		id, ok := <-okChan
		if !ok {
			fmt.Printf("[主线程]okChan不ok\n")
		}
		if id <= 0 {
			if downloadErr == nil {
				cancel()
				downloadErr = errors.New(fmt.Sprintf("[主线程]任务%d下载失败！！下载即将结束。", id*-1))
				fmt.Println(downloadErr.Error())
			}
			errNum++
		}
		//if id > 0 {
		//	fmt.Printf("[主线程]任务%d已完成\n", id)
		//}
		completedNum++
		if downloadErr != nil {
			if errNum == task.MaxThreadNum {
				fmt.Println("下载已取消！")
				break
			}
		} else {
			if completedNum == len(task.File) {
				fmt.Println("下载完成！")
				break
			}
		}
	}

	cancel()
	CallClear()

	if downloadErr == nil {
		//组装文件
		err := mergeFile(task)
		if err != nil {
			return err
		}
	} else {
		//清除已下载的临时文件
		err := failClean(task)
		if err != nil {
			return err
		}
	}

	close(downloadQueue)
	close(okChan)

	return nil
}

func downloader(ctx context.Context, queue <-chan *filePart, id int, okChan chan int, task *downloadTask) {
	//fmt.Printf("[协程%d]已启动！\n", id)
	for {
		select {
		case <-ctx.Done():
			okChan <- 0
			//fmt.Printf("[协程%d]已正常关闭:收到cancel信号\n", id)
			return
		case part, ok := <-queue:
			if !ok {
				fmt.Printf("[协程%d]已正常关闭:queue已关闭\n", id)
			}
			//下载文件分片
			req := &http.Request{
				Method: http.MethodGet,
				URL:    task.Url,
				Header: map[string][]string{
					"Range": {fmt.Sprintf("bytes=%d-%d", part.Begin, part.End)},
				},
			}

			//debug
			//if part.ID == 15 {
			//	fmt.Printf("[协程%d]任务%d下载失败:(debug)\n", id, part.ID)
			//	okChan <- part.ID * -1
			//	return
			//}
			//debug

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				//fmt.Printf("[协程%d]任务%d下载失败:%s\n", id, part.ID, err.Error())
				okChan <- part.ID * -1
				return
			}
			data, err := ioutil.ReadAll(resp.Body)
			err = resp.Body.Close()
			if err != nil {
				//fmt.Printf("[协程%d]任务%d读取内容失败:%s\n", id, part.ID, err.Error())
				okChan <- part.ID * -1
				return
			}

			err = ioutil.WriteFile(part.TmpFilePath, data, 0666)
			if err != nil {
				//fmt.Printf("[协程%d]任务%d创建文件失败:%s\n", id, part.ID, err.Error())
				okChan <- part.ID * -1
				return
			}

			//fmt.Printf("[协程%d]任务%d已完成\n", id, part.ID)
			part.Finished = true
			atomic.AddUint64(&task.Downloaded, part.End-part.Begin+1)
			okChan <- part.ID
		}
	}
}

func downloadTaskDispatcher(ctx context.Context, task *downloadTask, queue chan *filePart) {
	for i, _ := range task.File {
		select {
		case <-ctx.Done():
			emptyQueue(queue)
			return
		case queue <- &task.File[i]:
			continue
		}
	}
}

func mergeFile(task *downloadTask) error {
	file, err := os.Create(task.SavePath)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, part := range task.File {
		tmpFile, err := ioutil.ReadFile(part.TmpFilePath)
		if err != nil {
			return err
		}
		_, err = file.Write(tmpFile)
		err = os.Remove(part.TmpFilePath)
		if err != nil {
			return err
		}
		fmt.Printf("[合并文件]已合并[%d/%d]\r", part.ID, len(task.File))
	}
	fmt.Println("文件已保存至：" + strings.Replace(task.SavePath, "./", "程序目录/", -1))
	return nil
}

func failClean(task *downloadTask) error {
	var err error
	for _, file := range task.File {
		if file.Finished == true {
			err = os.Remove(file.TmpFilePath)
		}
	}
	return err
}

func emptyQueue(c chan *filePart) {
	ok := false
	for {
		select {
		case <-c:
		default:
			ok = true
		}
		if ok {
			break
		}
	}
}

func showDownloadProgress(ctx context.Context, task *downloadTask) {
	percentage := 0.0
	bandwidth := 0.0
	timeA := time.Now()
	var da uint64 = 0
	for {
		select {
		case <-ctx.Done():
			fmt.Printf("已完成：100%%\r")
			return
		case <-time.Tick(500 * time.Millisecond):
			if task.Downloaded != da {
				bandwidth = float64((task.Downloaded-da)>>20) / (float64(time.Now().UnixMilli()-timeA.UnixMilli()) / 1000)
				percentage = (float64(task.Downloaded) / float64(task.Size)) * 100
				timeA = time.Now()
				da = task.Downloaded
				fmt.Printf("已完成：%.2f%%(%.2f MB/s)\r", percentage, bandwidth)
			}
		}
	}
}
