Rails.application.routes.draw do
  get "up" => "rails/health#show", as: :rails_health_check

  namespace :api do
    namespace :v1 do
      post "users/login", to: "users#login"
      get "users/auth", to: "users#auth"
      resources :users
    end
  end
end
