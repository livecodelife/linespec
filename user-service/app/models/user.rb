class User < ApplicationRecord
  has_secure_password

  validates :email, presence: true, uniqueness: true

  before_validation :generate_token, if: -> { token.blank? }

  private

  def generate_token
    self.token = SecureRandom.hex(16)
  end
end
