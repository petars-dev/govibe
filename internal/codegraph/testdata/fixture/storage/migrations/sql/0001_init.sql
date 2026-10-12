-- +goose Up
CREATE TABLE users (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email VARCHAR(255) NOT NULL COMMENT 'login name',
  -- A sensitive column: its default must never reach the graph.
  password_hash VARCHAR(255) NOT NULL DEFAULT 'none',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  UNIQUE KEY ux_users_email (email)
) ENGINE=InnoDB COMMENT='Accounts';

CREATE TABLE products (
  id INT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  sku VARCHAR(40) NOT NULL UNIQUE,
  name VARCHAR(100) NULL,
  price DECIMAL(10,2) NOT NULL DEFAULT 0.00
) ENGINE=InnoDB;

-- user_id looks like a reference to users, but nothing enforces it.
CREATE TABLE orders (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  total BIGINT NOT NULL DEFAULT 0,
  status VARCHAR(20) NOT NULL DEFAULT 'new',
  PRIMARY KEY (id),
  KEY ix_orders_user (user_id)
) ENGINE=InnoDB;

-- A junction table: two foreign keys that together are the primary key.
CREATE TABLE order_items (
  order_id BIGINT UNSIGNED NOT NULL,
  product_id INT NOT NULL,
  quantity INT NOT NULL DEFAULT 1,
  PRIMARY KEY (order_id, product_id),
  CONSTRAINT fk_items_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE ON UPDATE RESTRICT,
  FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE=InnoDB;

-- +goose Down
DROP TABLE order_items;
DROP TABLE orders;
DROP TABLE products;
DROP TABLE users;
