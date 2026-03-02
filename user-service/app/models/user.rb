class User < ApplicationRecord
  validates :email, presence: true, uniqueness: true
  validates :password, presence: true, on: :create

  before_validation :generate_token, if: -> { token.blank? }

  def authenticate(password)
    self.password == password ? self : false
  end

  private

  def generate_token
    self.token = SecureRandom.hex(16)
  end
end
