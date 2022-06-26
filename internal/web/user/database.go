// SPDX-License-Identifier: AGPL-3.0-or-later
package user

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

const (
	sqlCreateUserTable         = `create table if not exists user(id integer primary key autoincrement, username text not null unique, salt text not null, password text not null, alias text, permissions text)`
	sqlInsertUser              = `insert into user(username, salt, password, alias, permissions) values(?,?,?,?,?)`
	sqlQueryUserCount          = `select count(*) from user`
	sqlQueryAllUser            = `select id, username, alias, permissions from user`
	sqlQueryUserByUsername     = `select id, alias, permissions from user where username=?`
	sqlQueryPasswordByUsername = `select salt, password from user where username=?`
	sqlUpdateUser              = `update user set username=?, salt=?, password=?, alias=?, permissions=? where id=?`
	sqlDeleteUser              = `delete from user where id=?`
)

func (w *Worker) initUserTable() (err error) {
	os.MkdirAll(filepath.Dir(w.dbName), os.ModeDir)
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()

	_, err = db.Exec(sqlCreateUserTable)
	if err != nil {
		return
	}
	return
}

func (w *Worker) noUser() bool {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return false
	}
	defer db.Close()
	stmt, err := db.Prepare(sqlQueryUserCount)
	if err != nil {
		return false
	}
	defer stmt.Close()

	var count int
	if err = stmt.QueryRow().Scan(&count); err != nil {
		return false
	}
	return count == 0
}

func (w *Worker) createUser(username, password, alias, permissions string) (err error) {
	salt := uuid.NewString()
	sum := sha256.Sum256([]byte(salt + password))
	hash := hex.EncodeToString(sum[:])

	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()

	stmt, err := db.Prepare(sqlInsertUser)
	if err != nil {
		return
	}
	defer stmt.Close()

	_, err = stmt.Exec(username, salt, hash, alias, permissions)
	if err != nil {
		return
	}
	return

}

func (w *Worker) checkUserPassword(username, password string) (ok bool, err error) {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()
	stmt, err := db.Prepare(sqlQueryPasswordByUsername)
	if err != nil {
		return
	}
	defer stmt.Close()

	var salt, hash string
	if err = stmt.QueryRow(username).Scan(&salt, &hash); err != nil {
		return
	}
	sum := sha256.Sum256([]byte(salt + password))
	ok = (hash == hex.EncodeToString(sum[:]))
	return
}

func (w *Worker) queryUserByUsername(username string) (user User, err error) {
	user.Username = username
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()
	stmt, err := db.Prepare(sqlQueryUserByUsername)
	if err != nil {
		return
	}
	defer stmt.Close()

	if err = stmt.QueryRow(user.Username).Scan(&user.UserID, &user.AliasName, &user.Permissions); err != nil {
		return
	}
	return
}

func (w *Worker) queryAllUser() (users []User, err error) {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return
	}
	defer db.Close()

	stmt, err := db.Prepare(sqlQueryAllUser)
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
		var user User
		err = rows.Scan(&user.UserID, &user.Username, &user.AliasName, &user.Permissions)
		if err != nil {
			return
		}
		users = append(users, user)
	}
	err = rows.Err()
	if err != nil {
		return
	}
	return
}

func (w *Worker) updateUserInfo(id uint64, username, password, alias, permissions string) bool {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return false
	}
	defer db.Close()

	stmt, err := db.Prepare(sqlUpdateUser)
	if err != nil {
		return false
	}
	defer stmt.Close()

	salt := uuid.NewString()
	sum := sha256.Sum256([]byte(salt + password))
	hash := hex.EncodeToString(sum[:])

	result, err := stmt.Exec(username, salt, hash, alias, permissions, id)
	if err != nil {
		return false
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false
	}
	return affected == 1
}

func (w *Worker) deleteUser(id uint64) bool {
	db, err := sql.Open("sqlite3", w.dbName)
	if err != nil {
		return false
	}
	defer db.Close()

	stmt, err := db.Prepare(sqlDeleteUser)
	if err != nil {
		return false
	}
	defer stmt.Close()

	result, err := stmt.Exec(id)
	if err != nil {
		return false
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false
	}
	return affected == 1
}
