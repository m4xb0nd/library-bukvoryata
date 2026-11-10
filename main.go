package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NavItem struct {
	Href, Label, Key string
}

var publicNav = []NavItem{
	{"/", "Главная", "home"},
	{"/books", "Книги", "books"},
	{"/about", "О нас", "about"},
}

var adminNav = []NavItem{
	{"/readers", "Читатели", "readers"},
	{"/loans", "Выдача", "loans"},
}

// Page — данные, доступные в каждом шаблоне.
type Page struct {
	Title   string
	Active  string
	IsAdmin bool
	Nav     []NavItem
	Msg     string
	Err     string
	Next    string
	Data    any
}

type App struct {
	store    *Store
	tmpl     map[string]*template.Template
	password string
	staticFS string

	sessMu   sync.Mutex
	sessions map[string]time.Time
}

func main() {
	addr := flag.String("addr", env("LIBRARY_ADDR", ":8080"), "адрес сервера")
	dsn := flag.String("db", env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/library?sslmode=disable"),
		"адрес подключения к PostgreSQL")
	flag.Parse()

	password := os.Getenv("LIBRARY_PASSWORD")
	if password == "" {
		password = "admin"
		log.Println("ВНИМАНИЕ: пароль библиотекаря по умолчанию — «admin». Задайте LIBRARY_PASSWORD.")
	}

	store, err := OpenStore(*dsn)
	if err != nil {
		log.Fatal(err)
	}
	app := &App{
		store: store, password: password, staticFS: "static",
		sessions: map[string]time.Time{},
	}
	if err := app.loadTemplates("templates"); err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("Библиотека запущена: http://localhost%s", *addr)
	log.Fatal(srv.ListenAndServe())
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (a *App) loadTemplates(dir string) error {
	funcs := template.FuncMap{
		"date": func(t time.Time) string { return t.Format("02.01.2006") },
	}
	pages := []string{
		"home", "about", "books", "book", "book_new", "readers", "reader",
		"reader_new", "loans", "loan_new", "login", "error",
	}
	a.tmpl = map[string]*template.Template{}
	for _, p := range pages {
		t, err := template.New(p).Funcs(funcs).ParseFiles(
			filepath.Join(dir, "base.html"), filepath.Join(dir, p+".html"))
		if err != nil {
			return fmt.Errorf("шаблон %s: %w", p, err)
		}
		a.tmpl[p] = t
	}
	return nil
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(a.staticFS))))

	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /about", a.about)
	mux.HandleFunc("GET /books", a.books)
	mux.HandleFunc("GET /books/{id}", a.book)
	mux.HandleFunc("GET /login", a.loginForm)
	mux.HandleFunc("POST /login", a.login)
	mux.HandleFunc("POST /logout", a.logout)

	mux.HandleFunc("GET /books/new", a.admin(a.bookNewForm))
	mux.HandleFunc("POST /books/new", a.admin(a.bookNew))
	mux.HandleFunc("POST /books/{id}/delete", a.admin(a.bookDelete))
	mux.HandleFunc("GET /readers", a.admin(a.readers))
	mux.HandleFunc("GET /readers/new", a.admin(a.readerNewForm))
	mux.HandleFunc("POST /readers/new", a.admin(a.readerNew))
	mux.HandleFunc("GET /readers/{id}", a.admin(a.reader))
	mux.HandleFunc("GET /loans", a.admin(a.loans))
	mux.HandleFunc("GET /loans/new", a.admin(a.loanNewForm))
	mux.HandleFunc("POST /loans/new", a.admin(a.loanNew))
	mux.HandleFunc("POST /loans/{id}/return", a.admin(a.loanReturn))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.fail(w, r, http.StatusNotFound, "Страница не найдена.")
	})
	return limitBody(mux)
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

func (a *App) render(w http.ResponseWriter, r *http.Request, name, title, active string, data any, status int) {
	isAdmin := a.isAdmin(r)
	nav := publicNav
	if isAdmin {
		nav = append(append([]NavItem{}, publicNav...), adminNav...)
	}
	p := Page{
		Title: title, Active: active, IsAdmin: isAdmin,
		Nav: nav, Msg: r.URL.Query().Get("msg"), Err: r.URL.Query().Get("err"),
		Next: r.URL.Path, Data: data,
	}
	var buf bytes.Buffer
	if err := a.tmpl[name].ExecuteTemplate(&buf, "base", p); err != nil {
		log.Printf("шаблон %s: %v", name, err)
		http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (a *App) fail(w http.ResponseWriter, r *http.Request, status int, msg string) {
	a.render(w, r, "error", "Ошибка", "", msg, status)
}

func redirect(w http.ResponseWriter, r *http.Request, to, msg, errText string) {
	q := url.Values{}
	if msg != "" {
		q.Set("msg", msg)
	}
	if errText != "" {
		q.Set("err", errText)
	}
	if len(q) > 0 {
		sep := "?"
		if strings.Contains(to, "?") {
			sep = "&"
		}
		to += sep + q.Encode()
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func msgOf(err error) string {
	if IsUserErr(err) {
		return err.Error()
	}
	log.Println("ошибка:", err)
	return "Внутренняя ошибка. Попробуйте ещё раз позже."
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func pathID(r *http.Request) int { return atoi(r.PathValue("id")) }


const sessionCookie = "library_session"

func (a *App) isAdmin(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	a.sessMu.Lock()
	defer a.sessMu.Unlock()
	exp, ok := a.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(a.sessions, c.Value)
		return false
	}
	return true
}

func (a *App) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.isAdmin(r) {
			next := "/"
			if r.Method == http.MethodGet {
				next = r.URL.RequestURI()
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
			return
		}
		h(w, r)
	}
}

func safeNext(next string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, "\\") {
		return next
	}
	return "/"
}

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "login", "Вход для библиотекаря", "", safeNext(r.URL.Query().Get("next")), http.StatusOK)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(a.password)) != 1 {
		time.Sleep(500 * time.Millisecond) 
		redirect(w, r, "/login?next="+url.QueryEscape(next), "", "Неверный пароль.")
		return
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		a.fail(w, r, http.StatusInternalServerError, "Не удалось создать сессию.")
		return
	}
	token := hex.EncodeToString(raw)
	a.sessMu.Lock()
	a.sessions[token] = time.Now().Add(8 * time.Hour)
	a.sessMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", MaxAge: 8 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.sessMu.Lock()
		delete(a.sessions, c.Value)
		a.sessMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	redirect(w, r, "/", "Вы вышли из режима библиотекаря.", "")
}

func (a *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Println("ошибка:", err)
	a.fail(w, r, http.StatusInternalServerError, "Внутренняя ошибка. Попробуйте ещё раз позже.")
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	st, err := a.store.Stats()
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "home", "Главная", "home", st, http.StatusOK)
}

func (a *App) about(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "about", "О нас", "about", map[string]int{"Days": LoanDays, "Max": MaxActiveLoans}, http.StatusOK)
}

func (a *App) books(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	genre := atoi(r.URL.Query().Get("genre"))
	books, err := a.store.Books(q, genre)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	genres, err := a.store.Genres()
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "books", "Книги", "books", map[string]any{
		"Books": books, "Genres": genres, "Q": q, "Genre": genre,
	}, http.StatusOK)
}

func (a *App) book(w http.ResponseWriter, r *http.Request) {
	b, ok, err := a.store.Book(pathID(r))
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if !ok {
		a.fail(w, r, http.StatusNotFound, "Книга не найдена.")
		return
	}
	a.render(w, r, "book", b.Title, "books", b, http.StatusOK)
}

func (a *App) bookNewForm(w http.ResponseWriter, r *http.Request) {
	a.renderBookForm(w, r, map[string]string{"Copies": "1"}, http.StatusOK)
}

func (a *App) renderBookForm(w http.ResponseWriter, r *http.Request, f map[string]string, status int) {
	genres, err := a.store.Genres()
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "book_new", "Новая книга", "books", map[string]any{"Genres": genres, "F": f}, status)
}

func (a *App) bookNew(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{
		"Title": r.FormValue("title"), "Author": r.FormValue("author"),
		"Genre": r.FormValue("genre"), "Year": r.FormValue("year"),
		"Copies": r.FormValue("copies"), "ISBN": r.FormValue("isbn"),
		"Description": r.FormValue("description"),
	}
	id, err := a.store.AddBook(f["Title"], f["Author"], atoi(f["Genre"]), atoi(f["Year"]),
		atoi(f["Copies"]), f["ISBN"], f["Description"])
	if err != nil {
		q := r.URL.Query()
		q.Set("err", msgOf(err))
		r.URL.RawQuery = q.Encode()
		a.renderBookForm(w, r, f, http.StatusUnprocessableEntity)
		return
	}
	redirect(w, r, "/books/"+strconv.Itoa(id), "Книга добавлена в каталог.", "")
}

func (a *App) bookDelete(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	if err := a.store.DeleteBook(id); err != nil {
		redirect(w, r, "/books/"+strconv.Itoa(id), "", msgOf(err))
		return
	}
	redirect(w, r, "/books", "Книга удалена из каталога.", "")
}

func (a *App) readers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	readers, err := a.store.Readers(q)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "readers", "Читатели", "readers", map[string]any{"Readers": readers, "Q": q}, http.StatusOK)
}

func (a *App) readerNewForm(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, "reader_new", "Новый читатель", "readers", map[string]string{}, http.StatusOK)
}

func (a *App) readerNew(w http.ResponseWriter, r *http.Request) {
	f := map[string]string{
		"FullName": r.FormValue("full_name"), "Phone": r.FormValue("phone"), "Email": r.FormValue("email"),
	}
	id, err := a.store.AddReader(f["FullName"], f["Phone"], f["Email"])
	if err != nil {
		q := r.URL.Query()
		q.Set("err", msgOf(err))
		r.URL.RawQuery = q.Encode()
		a.render(w, r, "reader_new", "Новый читатель", "readers", f, http.StatusUnprocessableEntity)
		return
	}
	redirect(w, r, "/readers/"+strconv.Itoa(id), "Читатель зарегистрирован, билет оформлен.", "")
}

func (a *App) reader(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	rd, ok, err := a.store.Reader(id)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if !ok {
		a.fail(w, r, http.StatusNotFound, "Читатель не найден.")
		return
	}
	loans, err := a.store.Loans(id, false)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "reader", rd.FullName, "readers", map[string]any{"Reader": rd, "Loans": loans}, http.StatusOK)
}

func (a *App) loans(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "1"
	loans, err := a.store.Loans(0, !all)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "loans", "Выдача книг", "loans", map[string]any{"Loans": loans, "All": all}, http.StatusOK)
}

func (a *App) renderLoanForm(w http.ResponseWriter, r *http.Request, bookID, readerID, status int) {
	all, err := a.store.Books("", 0)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	var books []BookView
	for _, b := range all {
		if b.Available > 0 {
			books = append(books, b)
		}
	}
	readers, err := a.store.Readers("")
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.render(w, r, "loan_new", "Выдать книгу", "loans", map[string]any{
		"Books": books, "Readers": readers, "Book": bookID, "Reader": readerID, "Days": LoanDays,
	}, status)
}

func (a *App) loanNewForm(w http.ResponseWriter, r *http.Request) {
	a.renderLoanForm(w, r, atoi(r.URL.Query().Get("book")), atoi(r.URL.Query().Get("reader")), http.StatusOK)
}

func (a *App) loanNew(w http.ResponseWriter, r *http.Request) {
	bookID, readerID := atoi(r.FormValue("book")), atoi(r.FormValue("reader"))
	if err := a.store.IssueBook(bookID, readerID); err != nil {
		q := r.URL.Query()
		q.Set("err", msgOf(err))
		r.URL.RawQuery = q.Encode()
		a.renderLoanForm(w, r, bookID, readerID, http.StatusUnprocessableEntity)
		return
	}
	redirect(w, r, "/loans", "Книга выдана.", "")
}

func (a *App) loanReturn(w http.ResponseWriter, r *http.Request) {
	if err := a.store.ReturnLoan(pathID(r)); err != nil {
		redirect(w, r, "/loans", "", msgOf(err))
		return
	}
	redirect(w, r, "/loans", "Возврат книги оформлен.", "")
}
