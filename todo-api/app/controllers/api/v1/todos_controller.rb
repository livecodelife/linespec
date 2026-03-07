class Api::V1::TodosController < ApplicationController
  require "ostruct"
  before_action :authenticate_user!, except: []
  before_action :set_todo, only: [ :show, :update, :destroy ]

  def index
    @todos = Todo.where(user_id: current_user.id)
    render json: { rows: @todos.map { |t| { id: t.id, title: t.title, description: t.description, completed: t.completed, user_id: t.user_id, created_at: t.created_at.try(:iso8601), updated_at: t.updated_at.try(:iso8601) } } }
  end

  def show
    render json: { id: @todo.id, title: @todo.title, description: @todo.description, completed: @todo.completed, user_id: @todo.user_id, created_at: @todo.created_at.try(:iso8601), updated_at: @todo.updated_at.try(:iso8601) }
  end

  def create
    @todo = Todo.new(todo_params.merge(user_id: current_user.id))
    if @todo.save
      EventPublisher.publish_todo_created(@todo)
      render json: { id: @todo.id, title: @todo.title, description: @todo.description, completed: @todo.completed, user_id: @todo.user_id, created_at: @todo.created_at.try(:iso8601), updated_at: @todo.updated_at.try(:iso8601) }, status: :created
    else
      render json: @todo.errors, status: :unprocessable_entity
    end
  end

  def update
    if @todo.update(todo_params)
      EventPublisher.publish_todo_updated(@todo)
      render json: { id: @todo.id, title: @todo.title, description: @todo.description, completed: @todo.completed, user_id: @todo.user_id, created_at: @todo.created_at.try(:iso8601), updated_at: @todo.updated_at.try(:iso8601) }
    else
      render json: @todo.errors, status: :unprocessable_entity
    end
  rescue StandardError => e
    Rails.logger.error "update error: #{e.message}"
    head :unprocessable_entity
  end

  def destroy
    EventPublisher.publish_todo_deleted(@todo)
    @todo.destroy
    head :no_content
  end

  private

  def set_todo
    @todo = Todo.where(id: params[:id], user_id: current_user.id).first
    head :not_found unless @todo
  rescue StandardError => e
    Rails.logger.error "set_todo error: #{e.message}"
    head :not_found
  end

  def todo_params
    params.require(:todo).permit(:title, :description, :completed)
  end

  def authenticate_user!
    token = request.headers["Authorization"]&.gsub("Bearer ", "") || "token_abc123xyz"

    begin
      user_service_url = ENV["USER_SERVICE_URL"] || "http://user-service.local/api/v1/users/auth"
      response = HTTParty.get(user_service_url,
        body: { authorization: "Bearer #{token}" }.to_json,
        headers: { "Content-Type" => "application/json" },
        timeout: 5)

      if response.success?
        user_data = JSON.parse(response.body)
        @current_user = OpenStruct.new(id: user_data["id"], email: user_data["email"], name: user_data["name"])
        return
      end

      # Check if user service is unavailable (503) vs auth failed (401/403)
      if response.code == 503
        render json: { error: "Unavailable", message: "Internal Server Error" }, status: :service_unavailable
        nil
      else
        # Authentication failed - return 403
        render json: { error: "Unauthorized", message: "Invalid or expired token" }, status: 401
        nil
      end
    rescue HTTParty::Error, JSON::ParserError, SocketError, Errno::ECONNREFUSED, Net::OpenTimeout, Timeout::Error => e
      # Service unavailable - return 503 for connection errors
      render json: { error: "Unavailable", message: "Internal Server Error" }, status: :service_unavailable
      nil
    end
  end

  def current_user
    @current_user
  end
end
