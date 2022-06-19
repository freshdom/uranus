// SPDX-License-Identifier: AGPL-3.0-or-later
package background

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"uranus/internal/config"
	"uranus/pkg/connector"
	"uranus/pkg/process"
	"uranus/pkg/status"
	"uranus/pkg/watchdog"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
)

const (
	// 初始化时创建数据表添加索引
	sqlCreateTable = `create table if not exists process(id integer primary key autoincrement, cmd blob not null unique, workdir text not null, binary text not null, argv text not null, count integer not null, judge integer not null, status integer not null)`
	sqlCreateIndex = `create unique index if not exists process_cmd_idx on process (cmd)`

	// 上报进程审计事件(可信进程不上报此事件)时更新计数和审计状态(放行或是阻止)
	sqlUpdateCount = `update process set count=count+1,judge=? where cmd=?`

	// 更新计数时发现命令没有执行过,插入新命令.
	sqlInsert = `insert into process(cmd,workdir,binary,argv,count,judge,status) values(?,?,?,?,1,?,0)`

	// 查询允许执行的命令,初始化 hackernel
	sqlQueryStatusAllow = `select cmd from process where status=2`
)

type ProcessWorker struct {
	dbName string

	running bool
	wg      sync.WaitGroup
	conn    connector.Connector
	config  *config.Config
}

func NewProcessWorker(dbName string) *ProcessWorker {
	worker := ProcessWorker{
		dbName: dbName,
	}
	return &worker
}

func (w *ProcessWorker) initDB() (err error) {
	os.MkdirAll(filepath.Dir(w.dbName), os.ModeDir)
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()

	_, err = db.Exec(sqlCreateTable)
	if err != nil {
		return
	}

	_, err = db.Exec(sqlCreateIndex)
	if err != nil {
		return
	}

	w.config, err = config.New(w.dbName)
	if err != nil {
		return
	}
	return
}

func (w *ProcessWorker) setTrustedCmd(cmd string) (err error) {
	data := map[string]string{
		"type": "user::proc::trusted::insert",
		"cmd":  cmd,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	err = w.conn.Send(string(b))
	if err != nil {
		return
	}
	return
}

func (w *ProcessWorker) initTrustedCmd() (err error) {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()

	stmt, err := db.Prepare(sqlQueryStatusAllow)
	if err != nil {
		return
	}
	defer stmt.Close()
	rows, err := stmt.Query()
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var cmd string
		err = rows.Scan(&cmd)
		if err != nil {
			return
		}
		w.setTrustedCmd(cmd)
	}
	err = rows.Err()
	if err != nil {
		return
	}
	return
}

func (w *ProcessWorker) updateCmd(cmd string, judge int) (err error) {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()

	stmt, err := db.Prepare(sqlUpdateCount)
	if err != nil {
		return
	}
	defer stmt.Close()
	result, err := stmt.Exec(judge, cmd)
	if err != nil {
		return
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 0 {
		return
	}

	stmt, err = db.Prepare(sqlInsert)
	if err != nil {
		return
	}
	defer stmt.Close()
	workdir, binary, argv := process.SplitCmd(cmd)
	_, err = stmt.Exec(cmd, workdir, binary, argv, judge)
	if err != nil {
		return
	}
	return
}

func (w *ProcessWorker) run() {
	defer w.wg.Done()
	dog := watchdog.New(10*time.Second, func() {
		logrus.Error("osinfo::report timeout")
		syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	})
	for w.running {
		msg, err := w.conn.Recv()

		if !w.running {
			break
		}

		if err != nil {
			logrus.Error(err)
			syscall.Kill(syscall.Getpid(), syscall.SIGINT)
			continue
		}

		event := struct {
			Type  string `json:"type"`
			Cmd   string `json:"cmd"`
			Judge int    `json:"judge"`
		}{}

		err = json.Unmarshal([]byte(msg), &event)
		if err != nil {
			logrus.Error(err)
			syscall.Kill(syscall.Getpid(), syscall.SIGINT)
			continue
		}
		switch event.Type {
		case "audit::proc::report":
			err = w.updateCmd(event.Cmd, event.Judge)
			if err != nil {
				logrus.Error(err)
				syscall.Kill(syscall.Getpid(), syscall.SIGINT)
			}
		case "osinfo::report":
			dog.Kick()
		}
	}
}

func (w *ProcessWorker) Start() (err error) {
	w.running = true
	err = w.conn.Connect()
	if err != nil {
		return
	}
	err = w.initDB()
	if err != nil {
		return
	}

	coreStatus, err := w.config.GetInteger("proc::core::status")
	if err == nil && coreStatus == status.ProcessCoreEnable {
		err = w.conn.Send(`{"type":"user::proc::enable"}`)
		if err != nil {
			return
		}
	}

	err = w.conn.Send(`{"type":"user::msg::sub","section":"audit::proc::report"}`)
	if err != nil {
		return
	}
	err = w.conn.Send(`{"type":"user::msg::sub","section":"osinfo::report"}`)
	if err != nil {
		return
	}

	err = w.initTrustedCmd()
	if err != nil {
		return
	}

	w.wg.Add(1)
	go w.run()
	return
}

func (w *ProcessWorker) Stop() (err error) {
	err = w.conn.Send(`{"type":"user::msg::unsub","section":"audit::proc::report"}`)
	if err != nil {
		return
	}
	err = w.conn.Send(`{"type":"user::msg::unsub","section":"osinfo::report"}`)
	if err != nil {
		return
	}

	coreStatus, err := w.config.GetInteger("proc::core::status")
	if err == nil && coreStatus == status.ProcessCoreEnable {
		err = w.conn.Send(`{"type":"user::proc::disable"}`)
		if err != nil {
			return
		}
	}

	time.Sleep(time.Second)
	w.running = false
	err = w.conn.Shutdown(time.Now())
	if err != nil {
		return
	}
	w.wg.Wait()
	w.conn.Close()
	return
}
