CREATE USER IF NOT EXISTS 'todo_user'@'%' IDENTIFIED BY 'todo_password';
GRANT ALL PRIVILEGES ON todo_api_development.* TO 'todo_user'@'%';
GRANT ALL PRIVILEGES ON todo_api_test.* TO 'todo_user'@'%';
FLUSH PRIVILEGES;
