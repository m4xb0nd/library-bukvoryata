CREATE TABLE genres (
    genre_id   SERIAL       PRIMARY KEY,
    name       VARCHAR(100) NOT NULL UNIQUE
);

CREATE TABLE authors (
    author_id  SERIAL       PRIMARY KEY,
    full_name  VARCHAR(150) NOT NULL UNIQUE
);

CREATE TABLE books (
    book_id           SERIAL       PRIMARY KEY,
    title             VARCHAR(200) NOT NULL,
    author_id         INTEGER      NOT NULL REFERENCES authors (author_id),
    genre_id          INTEGER      NOT NULL REFERENCES genres (genre_id),
    publication_year  SMALLINT     NOT NULL CHECK (publication_year >= 1000),
    isbn              VARCHAR(20),
    description       TEXT,
    copies_total      SMALLINT     NOT NULL CHECK (copies_total BETWEEN 1 AND 1000)
);

CREATE TABLE readers (
    reader_id      SERIAL       PRIMARY KEY,
    ticket_number  VARCHAR(20)  NOT NULL UNIQUE,  
    full_name      VARCHAR(150) NOT NULL,
    phone          VARCHAR(30),
    email          VARCHAR(100),
    registered_at  TIMESTAMPTZ   NOT NULL DEFAULT now()
);

CREATE TABLE loans (
    loan_id      SERIAL    PRIMARY KEY,
    book_id      INTEGER   NOT NULL REFERENCES books (book_id)     ON DELETE RESTRICT,
    reader_id    INTEGER   NOT NULL REFERENCES readers (reader_id) ON DELETE RESTRICT,
    issued_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    due_at       TIMESTAMPTZ NOT NULL,            
    returned_at  TIMESTAMPTZ,                    
    CHECK (due_at > issued_at),
    CHECK (returned_at IS NULL OR returned_at >= issued_at)
);

CREATE UNIQUE INDEX ux_loans_active_pair ON loans (book_id, reader_id) WHERE returned_at IS NULL;

CREATE INDEX ix_books_author   ON books (author_id);
CREATE INDEX ix_books_genre    ON books (genre_id);
CREATE INDEX ix_books_title    ON books (lower(title));
CREATE INDEX ix_loans_reader   ON loans (reader_id);
CREATE INDEX ix_loans_book     ON loans (book_id);
CREATE INDEX ix_loans_active   ON loans (due_at) WHERE returned_at IS NULL;

CREATE VIEW book_availability AS
SELECT b.book_id,
       b.title,
       b.copies_total,
       b.copies_total - COUNT(l.loan_id) AS copies_available
FROM books b
LEFT JOIN loans l ON l.book_id = b.book_id AND l.returned_at IS NULL
GROUP BY b.book_id, b.title, b.copies_total;
