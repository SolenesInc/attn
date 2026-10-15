UPDATE sessions SET label = ''
WHERE agent = 'shell' AND (
    label = 'shell'
    OR (label GLOB 'shell [0-9]*' AND substr(label, 7) NOT GLOB '*[^0-9]*')
    OR (label <> '' AND instr(label, '/') = 0
        AND substr(rtrim(directory, '/'), -length(label)-1) = '/' || label)
    OR (label = '/' AND directory = '/')
);
