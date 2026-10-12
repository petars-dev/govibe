-- +goose Up
CREATE TABLE orders (id INT PRIMARY KEY, name VARCHAR(64) NOT NULL);
-- +goose Down
DROP TABLE orders;
