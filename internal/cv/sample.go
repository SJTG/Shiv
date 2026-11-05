package cv

// Sample devuelve un CV de ejemplo, con datos inventados, para probar la app
// y la plantilla sin importar un PDF ni gastar llamadas a la IA.
func Sample() *CV {
	c := &CV{
		Basics: Basics{
			Name:     "Ada Ejemplo",
			Email:    "ada@example.com",
			Phone:    "+54 11 0000-0000",
			Location: "Buenos Aires, Argentina",
			Links: []Link{
				{Label: "LinkedIn", URL: "linkedin.com/in/ada-ejemplo"},
				{Label: "GitHub", URL: "github.com/ada-ejemplo"},
			},
		},
		Summary: "Desarrolladora backend con experiencia construyendo APIs y herramientas internas. Me interesa el trabajo con datos y la automatización de procesos.",
		Education: []Education{{
			Institution: "Universidad Ejemplo",
			Degree:      "Licenciatura en Sistemas",
			Period:      "2021 - 2025",
			Details:     []string{"Tesis sobre sistemas de recomendación"},
		}},
		Experience: []Experience{
			{
				Company: "Acme Software",
				Role:    "Desarrolladora Backend",
				Period:  "2023 - Actualidad",
				Bullets: []Bullet{
					{Text: "Desarrollé servicios REST en Go para el procesamiento de pagos."},
					{Text: "Mejoré los tiempos de respuesta de las consultas a Postgres agregando índices."},
					{Text: "Coordiné las revisiones de código del equipo de tres personas."},
				},
			},
			{
				Company: "Tienda Demo",
				Role:    "Desarrolladora Junior",
				Period:  "2022 - 2023",
				Bullets: []Bullet{
					{Text: "Mantuve la tienda online y corregí errores de producción."},
					{Text: "Automaticé reportes semanales con scripts en Python."},
				},
			},
		},
		Projects: []Project{{
			Name:    "Gestor de gastos",
			Bullets: []Bullet{{Text: "App web personal para registrar gastos, hecha con Go y SQLite."}},
		}},
		Skills: []SkillGroup{
			{Category: "Lenguajes", Items: []string{"Go", "Python", "SQL"}},
			{Category: "Herramientas", Items: []string{"Git", "Docker", "Postgres"}},
		},
	}
	if err := c.Prepare(); err != nil {
		panic(err)
	}
	return c
}
