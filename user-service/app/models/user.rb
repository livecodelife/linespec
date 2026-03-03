class User < ApplicationRecord
  validates :email, presence: true, uniqueness: true
  validates :password, presence: true, on: :create

  before_validation :generate_token, if: -> { token.blank? }

  def authenticate(password)
    BCrypt::Password.new(password_digest) == password
  end

  private

  def generate_token
    self.token = SecureRandom.hex(16)
  end
end
