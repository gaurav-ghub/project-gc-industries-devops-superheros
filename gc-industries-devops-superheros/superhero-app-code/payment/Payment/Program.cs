var builder = WebApplication.CreateBuilder(args);
var app = builder.Build();

// Health probe
app.MapGet("/health", () => Results.Ok(new
{
    service = "payment",
    status = "ok"
}));

// Payment endpoint
app.MapPost("/pay", () => Results.Ok(new
{
    success = true,
    status = "success",
    message = "payment processed successfully"
}));

app.Run("http://0.0.0.0:8084");


// test comment for pipeline
// another test comment for pipeline

