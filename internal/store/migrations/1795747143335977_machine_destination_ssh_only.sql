DROP INDEX machines_destination;
CREATE UNIQUE INDEX machines_destination ON machines(destination) WHERE transport = 'ssh';
