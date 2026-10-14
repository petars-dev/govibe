-- +goose Up
CREATE TABLE orders (
  id BIGINT NOT NULL,
  total INT NOT NULL,
  PRIMARY KEY (id)
);

-- A table with no primary key.
CREATE TABLE audit_log (
  at TIMESTAMP NOT NULL,
  what VARCHAR(100) NOT NULL
);
