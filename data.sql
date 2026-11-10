INSERT INTO genres (name) VALUES
    ('Роман'), ('Повесть'), ('Рассказ'), ('Поэзия'),
    ('Драма'), ('Научная литература'), ('Детская литература');

INSERT INTO authors (full_name) VALUES
    ('А. С. Пушкин'), ('М. Ю. Лермонтов'), ('Н. В. Гоголь'), ('Ф. М. Достоевский'),
    ('Л. Н. Толстой'), ('А. П. Чехов'), ('М. А. Булгаков');

INSERT INTO books (title, author_id, genre_id, publication_year, copies_total)
SELECT v.title, a.author_id, g.genre_id, v.year, v.copies
FROM (VALUES
    (1, 'Евгений Онегин', 'А. С. Пушкин', 'Поэзия', 1833, 3),
    (2, 'Капитанская дочка', 'А. С. Пушкин', 'Повесть', 1836, 3),
    (3, 'Герой нашего времени', 'М. Ю. Лермонтов', 'Роман', 1840, 2),
    (4, 'Ревизор', 'Н. В. Гоголь', 'Драма', 1836, 2),
    (5, 'Преступление и наказание', 'Ф. М. Достоевский', 'Роман', 1866, 2),
    (6, 'Война и мир', 'Л. Н. Толстой', 'Роман', 1869, 2),
    (7, 'Вишнёвый сад', 'А. П. Чехов', 'Драма', 1904, 2),
    (8, 'Мастер и Маргарита', 'М. А. Булгаков', 'Роман', 1967, 1)
) AS v (ord, title, author, genre, year, copies)
JOIN authors a ON a.full_name = v.author
JOIN genres  g ON g.name      = v.genre
ORDER BY v.ord;   

WITH n AS (SELECT nextval(pg_get_serial_sequence('readers', 'reader_id')) AS id)
INSERT INTO readers (reader_id, ticket_number, full_name, phone)
SELECT id, 'ЧБ-' || lpad(id::text, 5, '0'), 'Иванов Иван Иванович', '+7 900 000-00-00' FROM n;
