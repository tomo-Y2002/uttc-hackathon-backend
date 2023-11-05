package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/joho/godotenv"
	"github.com/oklog/ulid"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/oklog/ulid"
)

type UserResForHTTPGet struct {
	Id   string `json:"id"`
	Name string `json:"name"`
	Age  int    `json:"age"`
}
type UserResForHTTPPost struct {
	Id   ulid.ULID `json:"id"`
	Name string    `json:"name"`
	Age  int       `json:"age"`
}

// ① GoプログラムからMySQLへ接続
var db *sql.DB

func init() {
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("エラー")
	}
	// ①-1
	mysqlUser := os.Getenv("MYSQL_USER")
	if mysqlUser == "" {
		log.Fatal("環境変数 MYSQL_USER が設定されていません")
	}
	mysqlUserPwd := os.Getenv("MYSQL_PASSWORD")
	if mysqlUserPwd == "" {
		log.Fatal("環境変数 MYSQL_PASSWORD が設定されていません")
	}
	mysqlDatabase := os.Getenv("MYSQL_DATABASE")
	if mysqlDatabase == "" {
		log.Fatal("環境変数 MYSQL_DATABASE が設定されていません")
	}
	mysqlHost := os.Getenv("MYSQL_HOST")
	if mysqlHost == "" {
		log.Fatal("環境変数 MYSQL_HOST が設定されていません")
	}

	// ①-2
	dsn := fmt.Sprintf("%s:%s@tcp(%s:3306)/%s", mysqlUser, mysqlUserPwd, mysqlHost, mysqlDatabase)
	_db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("fail: sql.Open, %v\n", err)
	}
	// ①-3
	if err := _db.Ping(); err != nil {
		log.Fatalf("fail: _db.Ping, %v\n", err)
	}
	db = _db
}

// ② /userでリクエストされたらnameパラメーターと一致する名前を持つレコードをJSON形式で返す
func handler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// ②-1
		name := r.URL.Query().Get("name")
		if name == "" {
			log.Println("fail: name is empty")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// ②-2
		rows, err := db.Query("SELECT id, name, age FROM user WHERE name = ?", name)
		if err != nil {
			log.Printf("fail: db.Query, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// ②-3
		users := make([]UserResForHTTPGet, 0)
		for rows.Next() {
			var u UserResForHTTPGet
			if err := rows.Scan(&u.Id, &u.Name, &u.Age); err != nil {
				log.Printf("fail: rows.Scan, %v\n", err)

				if err := rows.Close(); err != nil { // 500を返して終了するが、その前にrowsのClose処理が必要
					log.Printf("fail: rows.Close(), %v\n", err)
				}
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			users = append(users, u)
		}

		// ②-4
		bytes, err := json.Marshal(users)
		if err != nil {
			log.Printf("fail: json.Marshal, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes)
	case http.MethodPost:
		// requestの中身のjsonファイルを取得
		var u UserResForHTTPPost
		if err := json.NewDecoder(r.Body).Decode(&u); err != nil {
			log.Printf("fail: json.NewDecoder, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// age, nameのチェック
		if u.Name == "" || len(u.Name) > 50 {
			log.Printf("fail: name length-> %d\n", len(u.Name))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if u.Age < 20 || u.Age > 80 {
			log.Printf("fail: age range-> %d\n", u.Age)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// idの採番(採番とは割り当てること)
		entropy := rand.New(rand.NewSource(time.Now().UnixNano()))
		ms := ulid.Timestamp(time.Now())
		id, err := ulid.New(ms, entropy)
		if err != nil {
			log.Printf("fail: ulid.New, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		u.Id = id

		// データベースへの挿入
		tx, err := db.Begin()
		if err != nil {
			log.Printf("fail: db.Begin, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		const query = "INSERT INTO user (id, name, age) VALUES (?, ?, ?)"
		if _, err := tx.Exec(query, u.Id.String(), u.Name, u.Age); err != nil {
			tx.Rollback()
			log.Printf("fail: tx.Exec, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := tx.Commit(); err != nil {
			log.Printf("fail: tx.Commit, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// 応答
		w.WriteHeader(http.StatusOK)
		bytes, err := json.Marshal(u.Id)
		if err != nil {
			log.Printf("fail: json.Marshal, %v\n", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(bytes)

	default:
		log.Printf("fail: HTTP Method is %s\n", r.Method)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
}

func main() {
	// ② /userでリクエストされたらPostかGetで動作を変更する
	http.HandleFunc("/user", handler)

	// ③ Ctrl+CでHTTPサーバー停止時にDBをクローズする
	closeDBWithSysCall()

	// PORTで待ち受ける
	port := os.Getenv("PORT")
	if port == "" {
		log.Fatal("環境変数 PORT が設定されていません")
	}
	
	// 8000番ポートでリクエストを待ち受ける
	log.Println("Listening...")
	// if err := http.ListenAndServe(":8080", nil); err != nil {
	// 	log.Fatal(err)
	// }
	http.ListenAndServe(":8080", nil)
}

// ③ Ctrl+CでHTTPサーバー停止時にDBをクローズする
func closeDBWithSysCall() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		s := <-sig
		log.Printf("received syscall, %v", s)

		if err := db.Close(); err != nil {
			log.Fatal(err)
		}
		log.Printf("success: db.Close()")
		os.Exit(0)
	}()
}
