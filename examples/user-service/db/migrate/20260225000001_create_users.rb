class CreateUsers < ActiveRecord::Migration[7.2]
  def change
    create_table :users, if_not_exists: true do |t|
      t.string :name
      t.string :email
      t.string :password
      t.string :token
      t.timestamps
    end

    begin
      add_index :users, :email, unique: true
    rescue ActiveRecord::StatementInvalid
    end

    begin
      add_index :users, :token
    rescue ActiveRecord::StatementInvalid
    end
  end
end
