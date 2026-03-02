class Api::V1::UsersController < ApplicationController
  before_action :authenticate_user, except: [ :login, :create ]

  def create
    @user = User.new(user_params)
    if @user.save
      render json: {
        id: @user.id,
        name: @user.name,
        email: @user.email,
        created_at: @user.created_at
      }, status: :created
    elsif @user.errors[:email]&.any? { |e| e.include?("taken") }
      render json: { error: "Bad Request", message: "Email already exists" }, status: :conflict
    else
      render json: { error: "Bad Request", message: @user.errors.full_messages.join(", ") }, status: :bad_request
    end
  end

  def show
    @user = User.find_by(id: params[:id])
    if @user
      render json: {
        id: @user.id,
        name: @user.name,
        email: @user.email,
        created_at: @user.created_at
      }, status: :ok
    else
      render json: { error: "Not Found", message: "User with ID #{params[:id]} not found" }, status: :not_found
    end
  end

  def update
    @user = User.find_by(id: params[:id])
    if @user
      if @user.update(user_params)
        render json: {
          id: @user.id,
          name: @user.name,
          email: @user.email,
          created_at: @user.created_at
        }, status: :ok
      else
        render json: { error: "Bad Request", message: @user.errors.full_messages.join(", ") }, status: :bad_request
      end
    else
      render json: { error: "Not Found", message: "User with ID #{params[:id]} not found" }, status: :not_found
    end
  end

  def destroy
    @user = User.find_by(id: params[:id])
    if @user
      @user.destroy
      head :no_content
    else
      render json: { error: "Not Found", message: "User with ID #{params[:id]} not found" }, status: :not_found
    end
  end

  def login
    user = User.find_by(email: login_params[:email])
    if user && user.authenticate(login_params[:password])
      render json: {
        token: user.token,
        user: {
          id: user.id,
          name: user.name,
          email: user.email
        }
      }, status: :ok
    else
      render json: { error: "Unauthorized", message: "Invalid credentials" }, status: :unauthorized
    end
  end

  def auth
    render json: {
      id: @current_user.id,
      email: @current_user.email,
      name: @current_user.name
    }, status: :ok
  end

  private

  def user_params
    user_hash = params[:user]
    if user_hash&.dig(:password)
      params.require(:user).permit(:name, :email, :password)
    elsif params[:password]
      params.permit(:name, :email, :password)
    else
      (user_hash || params).permit(:name, :email, :password)
    end
  end

  def login_params
    user_hash = params[:user]
    if user_hash&.dig(:password)
      params.require(:user).permit(:email, :password)
    elsif params[:password]
      params.permit(:email, :password)
    else
      (user_hash || params).permit(:email, :password)
    end
  end
end
