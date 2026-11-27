SELECT DISTINCT u.id, t.name AS team FROM app.users AS u INNER JOIN app.teams AS t ON u.team_id = t.id WHERE t.active = 1 ORDER BY u.id DESC, t.name ASC;
