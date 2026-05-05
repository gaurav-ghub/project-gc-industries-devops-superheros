# -------- Build stage --------
FROM mcr.microsoft.com/dotnet/sdk:8.0 AS build
WORKDIR /src

COPY Payment/Payment.csproj Payment/
RUN dotnet restore Payment/Payment.csproj

COPY Payment/ Payment/
WORKDIR /src/Payment
RUN dotnet publish -c Release -o /app/publish

# -------- Runtime stage --------
FROM mcr.microsoft.com/dotnet/aspnet:8.0
WORKDIR /app
COPY --from=build /app/publish .

EXPOSE 8084
ENTRYPOINT ["dotnet", "Payment.dll"]
